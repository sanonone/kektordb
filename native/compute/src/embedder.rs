// Built-in ONNX embedder — loads all-MiniLM-L6-v2 (or compatible) ONNX model
// and exposes a C ABI for Go CGO integration.
//
// Thread safety:
//   MODEL: Mutex<Option<Arc<RwLock<ModelState>>>> — Mutex guards init/destroy,
//   Arc allows cloning ownership to multiple concurrent embedders, RwLock permits
//   concurrent read-only inference from multiple Go goroutines.
//
// Exported C functions:
//   kektordb_embed_init(model_path, tokenizer_path) -> 0/-1
//   kektordb_embed(text, out_vec, out_dim)         -> 0/-1
//   kektordb_free_embedding(ptr, len)               -> void
//   kektordb_embed_destroy()                        -> void

use std::collections::HashMap;
use std::ffi::CStr;
use std::os::raw::{c_char, c_int};
use std::sync::{Arc, Mutex, RwLock};

use candle_core::{DType, Device, Tensor};
use tokenizers::Tokenizer;

static MODEL: Mutex<Option<Arc<RwLock<ModelState>>>> = Mutex::new(None);

struct ModelState {
    model: candle_onnx::onnx::ModelProto,
    tokenizer: Tokenizer,
}

/// Initialize the embedder from ONNX model and tokenizer file paths.
/// Returns 0 on success, -1 on error.
/// Safe to call multiple times — subsequent calls are no-ops if already initialized.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_embed_init(
    model_path: *const c_char,
    tokenizer_path: *const c_char,
) -> c_int {
    let mp = match unsafe { CStr::from_ptr(model_path) }.to_str() {
        Ok(s) => s,
        Err(_) => return -1,
    };
    let tp = match unsafe { CStr::from_ptr(tokenizer_path) }.to_str() {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let model = match candle_onnx::read_file(mp) {
        Ok(m) => m,
        Err(_) => return -1,
    };
    let tokenizer = match Tokenizer::from_file(tp) {
        Ok(t) => t,
        Err(_) => return -1,
    };

    let mut guard = MODEL.lock().unwrap();
    if guard.is_some() {
        return 0; // Already initialized
    }
    *guard = Some(Arc::new(RwLock::new(ModelState { model, tokenizer })));
    0
}

/// Embed a UTF-8 text string and return a float32 vector (384 dimensions for all-MiniLM-L6-v2).
/// The caller must free the returned vector with kektordb_free_embedding(ptr, len).
/// Returns 0 on success, -1 if the model is not initialized or inference fails.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_embed(
    text: *const c_char,
    out_vec: *mut *mut f32,
    out_dim: *mut c_int,
) -> c_int {
    let text_str = match unsafe { CStr::from_ptr(text) }.to_str() {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let state_arc = {
        let guard = MODEL.lock().unwrap();
        match &*guard {
            Some(arc) => Arc::clone(arc),
            None => return -1,
        }
    };

    let state = state_arc.read().unwrap();
    let device = Device::Cpu;

    // Tokenize
    let encoding = match state.tokenizer.encode(text_str, true) {
        Ok(e) => e,
        Err(_) => return -1,
    };
    let tokens: Vec<i64> = encoding.get_ids().iter().map(|&id| id as i64).collect();
    let seq_len = tokens.len();

    // Build named input tensors (all I64 for this ONNX model)
    let input_ids = match Tensor::from_slice(&tokens, (1, seq_len), &device) {
        Ok(t) => t,
        Err(_) => return -1,
    };
    let attention_mask = match Tensor::ones((1, seq_len), DType::I64, &device) {
        Ok(t) => t,
        Err(_) => return -1,
    };
    let token_type_ids = match Tensor::zeros((1, seq_len), DType::I64, &device) {
        Ok(t) => t,
        Err(_) => return -1,
    };

    let mut inputs = HashMap::new();
    inputs.insert("input_ids".to_string(), input_ids);
    inputs.insert("attention_mask".to_string(), attention_mask);
    inputs.insert("token_type_ids".to_string(), token_type_ids);

    // Run inference
    let outputs = match candle_onnx::simple_eval(&state.model, inputs) {
        Ok(o) => o,
        Err(_) => return -1,
    };

    // Extract embedding — try common ONNX output names
    let last_hidden = match outputs
        .get("last_hidden_state")
        .or_else(|| outputs.get("sentence_embedding"))
        .or_else(|| outputs.get("output_0"))
        .or_else(|| outputs.values().next())
    {
        Some(t) => t,
        None => return -1,
    };

    // Mean pooling over sequence length → single vector
    let pooled = match last_hidden.mean(1) {
        Ok(t) => match t.squeeze(0) {
            Ok(t) => t,
            Err(_) => return -1,
        },
        Err(_) => return -1,
    };

    let dim = pooled.dims().first().copied().unwrap_or(0);

    // Copy to C heap. Ownership transfers to caller via kektordb_free_embedding.
    match pooled.to_vec1::<f32>() {
        Ok(vec_data) => {
            let ptr = vec_data.as_ptr() as *mut f32;
            std::mem::forget(vec_data);
            unsafe {
                *out_dim = dim as c_int;
                *out_vec = ptr;
            }
            0
        }
        Err(_) => -1,
    }
}

/// Free a vector allocated by kektordb_embed.
/// len must match the out_dim value returned by kektordb_embed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_free_embedding(ptr: *mut f32, len: c_int) {
    if ptr.is_null() || len <= 0 {
        return;
    }
    unsafe {
        let _ = Vec::from_raw_parts(ptr, len as usize, len as usize);
    }
}

/// Embed a batch of UTF-8 text strings in a single inference pass.
///
/// The texts are tokenized together, padded to the longest sequence in the
/// batch, and evaluated as one (count, max_seq) input. Pooling is mask-aware
/// (sum over real tokens / token count) so results are numerically equivalent
/// to calling kektordb_embed count times — the padding tokens never
/// contribute to the mean.
///
/// On success: *out_vecs is an array of `count` row pointers (each an
/// independent heap allocation of `*out_dim` floats), *out_count == count.
/// The caller must release the result with kektordb_free_embeddings.
/// Returns 0 on success, -1 if the model is not initialized or inference fails.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_embed_batch(
    texts: *const *const c_char,
    count: c_int,
    out_vecs: *mut *mut *mut f32,
    out_count: *mut c_int,
    out_dim: *mut c_int,
) -> c_int {
    if count <= 0 || texts.is_null() || out_vecs.is_null() || out_count.is_null() || out_dim.is_null()
    {
        return -1;
    }
    let n = count as usize;

    let mut strings = Vec::with_capacity(n);
    for i in 0..n {
        let ptr = unsafe { *texts.add(i) };
        let s = match unsafe { CStr::from_ptr(ptr) }.to_str() {
            Ok(s) => s,
            Err(_) => return -1,
        };
        strings.push(s.to_owned());
    }

    let state_arc = {
        let guard = MODEL.lock().unwrap();
        match &*guard {
            Some(arc) => Arc::clone(arc),
            None => return -1,
        }
    };

    let state = state_arc.read().unwrap();
    let device = Device::Cpu;

    // Tokenize the whole batch in one call (no truncation; pad to longest below).
    let encodings = match state.tokenizer.encode_batch(strings, true) {
        Ok(e) => e,
        Err(_) => return -1,
    };

    let max_seq = encodings.iter().map(|e| e.get_ids().len()).max().unwrap_or(0);
    if max_seq == 0 {
        return -1;
    }

    // Padding token id: prefer the tokenizer's [PAD] token, fall back to 0.
    let pad_id = state.tokenizer.token_to_id("[PAD]").unwrap_or(0) as i64;

    let mut input_ids = Vec::with_capacity(n * max_seq);
    let mut attn_mask = Vec::with_capacity(n * max_seq);
    for enc in &encodings {
        let ids: Vec<i64> = enc.get_ids().iter().map(|&id| id as i64).collect();
        let len = ids.len();
        input_ids.extend_from_slice(&ids);
        input_ids.resize(input_ids.len() + (max_seq - len), pad_id);
        attn_mask.extend(std::iter::repeat(1i64).take(len));
        attn_mask.extend(std::iter::repeat(0i64).take(max_seq - len));
    }

    let input_ids = match Tensor::from_vec(input_ids, (n, max_seq), &device) {
        Ok(t) => t,
        Err(_) => return -1,
    };
    let attn_mask = match Tensor::from_vec(attn_mask, (n, max_seq), &device) {
        Ok(t) => t,
        Err(_) => return -1,
    };
    let token_type_ids = match Tensor::zeros((n, max_seq), DType::I64, &device) {
        Ok(t) => t,
        Err(_) => return -1,
    };

    let mut inputs = HashMap::new();
    inputs.insert("input_ids".to_string(), input_ids);
    inputs.insert("attention_mask".to_string(), attn_mask.clone());
    inputs.insert("token_type_ids".to_string(), token_type_ids);

    // Run inference once for the whole batch.
    let outputs = match candle_onnx::simple_eval(&state.model, inputs) {
        Ok(o) => o,
        Err(_) => return -1,
    };

    let last_hidden = match outputs
        .get("last_hidden_state")
        .or_else(|| outputs.get("sentence_embedding"))
        .or_else(|| outputs.get("output_0"))
        .or_else(|| outputs.values().next())
    {
        Some(t) => t,
        None => return -1,
    };

    // Mask-aware mean pooling over the sequence dimension:
    // pooled = sum(hidden * mask) / sum(mask)  →  (n, dim).
    // For real (unpadded) tokens the mask is 1, so this is equivalent to the
    // plain mean(1) used by kektordb_embed on single texts.
    let pooled: Option<Tensor> = (|| {
        let hidden = last_hidden.to_dtype(DType::F32).ok()?;
        let mask = attn_mask.to_dtype(DType::F32).ok()?;
        let masked = hidden.broadcast_mul(&mask.unsqueeze(2).ok()?).ok()?;
        let summed = masked.sum(1).ok()?;
        let counts = mask.sum(1).ok()?;
        summed.broadcast_div(&counts.unsqueeze(1).ok()?).ok()
    })();
    let pooled = match pooled {
        Some(p) => p,
        None => return -1,
    };

    let dim = pooled.dims().last().copied().unwrap_or(0);
    if dim == 0 {
        return -1;
    }

    let flat = match pooled.flatten_all().and_then(|f| f.to_vec1::<f32>()) {
        Ok(v) => v,
        Err(_) => return -1,
    };

    // Each row becomes an independent heap allocation, mirroring
    // kektordb_embed's layout so kektordb_free_embeddings can release them.
    let mut rows: Vec<*mut f32> = Vec::with_capacity(n);
    for i in 0..n {
        let row: Vec<f32> = flat[i * dim..(i + 1) * dim].to_vec();
        let ptr = row.as_ptr() as *mut f32;
        std::mem::forget(row);
        rows.push(ptr);
    }

    unsafe {
        *out_vecs = rows.as_mut_ptr();
        *out_count = count;
        *out_dim = dim as c_int;
    }
    std::mem::forget(rows);
    0
}

/// Free the result of kektordb_embed_batch: an array of `count` row pointers,
/// each an independent allocation of `dim` floats.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_free_embeddings(vecs: *mut *mut f32, count: c_int, dim: c_int) {
    if vecs.is_null() || count <= 0 || dim <= 0 {
        return;
    }
    let n = count as usize;
    let d = dim as usize;
    unsafe {
        for i in 0..n {
            let row = *vecs.add(i);
            if !row.is_null() {
                let _ = Vec::from_raw_parts(row, d, d);
            }
        }
        let _ = Vec::from_raw_parts(vecs, n, n);
    }
}

/// Destroy the embedded model and free resources.
/// After calling this, kektordb_embed returns -1 until kektordb_embed_init is called again.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_embed_destroy() {
    let mut guard = MODEL.lock().unwrap();
    *guard = None;
}
