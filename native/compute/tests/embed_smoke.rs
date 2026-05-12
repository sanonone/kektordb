// Smoke test: verify candle-onnx can load all-MiniLM-L6-v2 and produce 384d embeddings.
// This gate must pass before any Go code is written.
//
// candle-onnx 0.8.4 uses simple_eval() with HashMap<String, Tensor>.
// tokenizers 0.21 uses from_file(), not from_pretrained().
//
// Run: PROTOC=/tmp/protoc/bin/protoc cargo test --test embed_smoke -- --test-threads=1
// Model path: /tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx
// Tokenizer:   /tmp/kektordb-test-models/tokenizer.json

use std::collections::HashMap;

use candle_core::{Device, Tensor, DType};
use tokenizers::Tokenizer;

const MODEL_PATH: &str = "/tmp/kektordb-test-models/all-MiniLM-L6-v2.onnx";
const TOKENIZER_PATH: &str = "/tmp/kektordb-test-models/tokenizer.json";

/// Load model, tokenize, forward, mean-pool, return Vec<f32>
fn embed(text: &str, model: &candle_onnx::onnx::ModelProto, tokenizer: &Tokenizer) -> Vec<f32> {
    let device = Device::Cpu;

    // Tokenize
    let encoding = tokenizer.encode(text, true).expect("Tokenization failed");
    let tokens: Vec<i64> = encoding.get_ids().iter().map(|&id| id as i64).collect();
    let seq_len = tokens.len();

    // Create inputs matching ONNX model input names (all I64 for this model)
    let input_ids = Tensor::from_slice(&tokens, (1, seq_len), &device).unwrap();
    let attention_mask = Tensor::ones((1, seq_len), DType::I64, &device).unwrap();
    let token_type_ids = Tensor::zeros((1, seq_len), DType::I64, &device).unwrap();

    // Build named inputs
    let mut inputs = HashMap::new();
    inputs.insert("input_ids".to_string(), input_ids);
    inputs.insert("attention_mask".to_string(), attention_mask);
    inputs.insert("token_type_ids".to_string(), token_type_ids);

    // Run inference
    let outputs = candle_onnx::simple_eval(model, inputs).expect("Inference failed");

    // Try common output names
    let last_hidden = outputs
        .get("last_hidden_state")
        .or_else(|| outputs.get("sentence_embedding"))
        .or_else(|| outputs.get("output_0"))
        .or_else(|| outputs.values().next())
        .expect("No output tensor found");

    // Mean pooling over sequence length (dim 1)
    let pooled = last_hidden
        .mean(1).expect("Mean pooling failed")
        .squeeze(0).expect("Squeeze failed");

    pooled.to_vec1::<f32>().expect("Failed to convert to vec")
}

#[test]
fn smoke_onnx_load_and_infer() {
    let model = candle_onnx::read_file(MODEL_PATH).expect("Failed to read ONNX file");
    let tokenizer = Tokenizer::from_file(TOKENIZER_PATH).expect("Failed to load tokenizer");

    let vec = embed("hello world", &model, &tokenizer);

    assert_eq!(vec.len(), 384, "Expected 384-dim embedding, got {}", vec.len());
    assert!(
        vec.iter().any(|&v| v.abs() > 1e-6),
        "Embedding should not be all zeros"
    );

    println!("OK: embedding dim={}, sample=[{:.6}, {:.6}, {:.6}...]",
        vec.len(), vec[0], vec[1], vec[2]);
}

#[test]
fn smoke_onnx_consistent_output() {
    let model = candle_onnx::read_file(MODEL_PATH).unwrap();
    let tokenizer = Tokenizer::from_file(TOKENIZER_PATH).unwrap();

    let v1 = embed("hello world", &model, &tokenizer);
    let v2 = embed("hello world", &model, &tokenizer);

    assert_eq!(v1.len(), 384);
    assert_eq!(v2.len(), 384);

    for i in 0..384 {
        assert!(
            (v1[i] - v2[i]).abs() < 1e-5,
            "Mismatch at index {}: {} vs {}", i, v1[i], v2[i]
        );
    }

    println!("OK: consistent output verified for 384 dimensions");
}

#[test]
fn smoke_onnx_short_and_long_text() {
    let model = candle_onnx::read_file(MODEL_PATH).unwrap();
    let tokenizer = Tokenizer::from_file(TOKENIZER_PATH).unwrap();

    let v_short = embed("hi", &model, &tokenizer);
    let v_long = embed("this is a much longer text that contains more tokens and should produce a different embedding vector", &model, &tokenizer);

    assert_eq!(v_short.len(), 384);
    assert_eq!(v_long.len(), 384);

    let diff: f32 = v_short.iter().zip(v_long.iter()).map(|(a, b)| (a - b).abs()).sum();
    assert!(diff > 0.01, "Short and long text embeddings are too similar (diff={})", diff);

    println!("OK: short vs long text produce distinct embeddings (diff={:.4})", diff);
}
