#ifndef KektorDBCompute_h
#define KektorDBCompute_h

#include <stddef.h>
#include <stdint.h>

// Distance computation (SIMD-accelerated for x86_64 and aarch64)
float       squared_euclidean_f32(const float *x, const float *y, size_t len);
float       dot_product_f32(const float *x, const float *y, size_t len);
float       squared_euclidean_f16(const uint16_t *x, const uint16_t *y, size_t len);
int32_t     dot_product_i8(const int8_t *x, const int8_t *y, size_t len);

// Built-in ONNX embedder
int  kektordb_embed_init(const char *model_path, const char *tokenizer_path);
int  kektordb_embed(const char *text, float **out_vec, int *out_dim);
void kektordb_free_embedding(float *ptr, int len);
void kektordb_embed_destroy(void);

#endif /* KektorDBCompute_h */
