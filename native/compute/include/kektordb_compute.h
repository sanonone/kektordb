#ifndef KektorDBCompute_h
#define KektorDBCompute_h

#include <stddef.h> // Per il tipo size_t
#include <stdint.h>

// Dichiarazione della nostra funzione Rust. La firma deve corrispondere
// a quella definita in lib.rs.
float squared_euclidean_f32(const float *x, const float *y, size_t len);
float dot_product_f32(const float *x, const float *y, size_t len);
float squared_euclidean_f16(const uint16_t *x, const uint16_t *y, size_t len);
int32_t dot_product_i8(const int8_t *x, const int8_t *y, size_t len);

#endif /* KektorDBCompute_h */
