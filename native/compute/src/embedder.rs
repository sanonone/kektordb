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

/// Destroy the embedded model and free resources.
/// After calling this, kektordb_embed returns -1 until kektordb_embed_init is called again.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn kektordb_embed_destroy() {
    let mut guard = MODEL.lock().unwrap();
    *guard = None;
}
