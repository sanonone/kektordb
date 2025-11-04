#![allow(non_snake_case)]
#![allow(unused_imports)]
#![allow(unsafe_op_in_unsafe_fn)]

use half::f16;
use std::mem;
use std::os::raw::{c_float, c_int, c_ushort}; // c_ushort per u16

// AGGIUNGI QUESTA RIGA:
#[cfg(target_arch = "aarch64")]
use std::arch::is_aarch64_feature_detected;

// --- Modulo per l'implementazione specifica di x86_64 ---
#[cfg(target_arch = "x86_64")]
mod x86_64_impl {
    use super::*;
    use std::arch::x86_64::*;

    // Incolla qui TUTTE le tue funzioni SIMD per x86_64:
    pub(super) unsafe fn reduce_sum_ps(v: __m256) -> f32 {
        let vhigh = _mm256_extractf128_ps(v, 1);
        let vlow = _mm256_castps256_ps128(v);
        let sum = _mm_add_ps(vlow, vhigh);
        let sumhigh = _mm_movehl_ps(sum, sum);
        let sum = _mm_add_ps(sum, sumhigh);
        let sumhigh = _mm_shuffle_ps(sum, sum, 1);
        let sum = _mm_add_ss(sum, sumhigh);
        _mm_cvtss_f32(sum)
    }

    #[target_feature(enable = "fma")]
    pub(super) unsafe fn squared_euclidean_f32_fma(
        x: *const f32,
        y: *const f32,
        len: usize,
    ) -> f32 {
        let mut sum_vec = _mm256_setzero_ps();
        // Removed aligned check for simplicity; use unaligned always (safe and often fast enough)
        let mut i = 0;
        while i + 8 <= len {
            let x_vec = _mm256_loadu_ps(x.add(i));
            let y_vec = _mm256_loadu_ps(y.add(i));
            let diff = _mm256_sub_ps(x_vec, y_vec);
            sum_vec = _mm256_fmadd_ps(diff, diff, sum_vec);
            i += 8;
        }

        let mut total_sum = reduce_sum_ps(sum_vec);

        // Unrolled remainder for better perf on small remainders
        let mut j = i;
        while j + 4 <= len {
            let d1 = *x.add(j) - *y.add(j);
            total_sum += d1 * d1;
            let d2 = *x.add(j + 1) - *y.add(j + 1);
            total_sum += d2 * d2;
            let d3 = *x.add(j + 2) - *y.add(j + 2);
            total_sum += d3 * d3;
            let d4 = *x.add(j + 3) - *y.add(j + 3);
            total_sum += d4 * d4;
            j += 4;
        }
        while j < len {
            let diff = *x.add(j) - *y.add(j);
            total_sum += diff * diff;
            j += 1;
        }
        total_sum
    }

    #[target_feature(enable = "fma")]
    pub(super) unsafe fn dot_product_f32_fma(x: *const f32, y: *const f32, len: usize) -> f32 {
        let mut sum_vec = _mm256_setzero_ps();
        let mut i = 0;
        while i + 8 <= len {
            let x_vec = _mm256_loadu_ps(x.add(i));
            let y_vec = _mm256_loadu_ps(y.add(i));
            sum_vec = _mm256_fmadd_ps(x_vec, y_vec, sum_vec);
            i += 8;
        }

        let mut total_sum = reduce_sum_ps(sum_vec);

        let mut j = i;
        while j + 4 <= len {
            total_sum += *x.add(j) * *y.add(j);
            total_sum += *x.add(j + 1) * *y.add(j + 1);
            total_sum += *x.add(j + 2) * *y.add(j + 2);
            total_sum += *x.add(j + 3) * *y.add(j + 3);
            j += 4;
        }
        while j < len {
            total_sum += *x.add(j) * *y.add(j);
            j += 1;
        }
        total_sum
    }

    #[target_feature(enable = "fma", enable = "f16c")]
    pub(super) unsafe fn squared_euclidean_f16_fma(
        x: *const u16,
        y: *const u16,
        len: usize,
    ) -> f32 {
        let mut sum_vec = _mm256_setzero_ps();
        let mut i = 0;
        while i + 8 <= len {
            let x_h = _mm_loadu_si128(x.add(i) as *const __m128i);
            let y_h = _mm_loadu_si128(y.add(i) as *const __m128i);
            let x_f = _mm256_cvtph_ps(x_h);
            let y_f = _mm256_cvtph_ps(y_h);
            let diff = _mm256_sub_ps(x_f, y_f);
            sum_vec = _mm256_fmadd_ps(diff, diff, sum_vec);
            i += 8;
        }

        let mut total_sum = reduce_sum_ps(sum_vec);

        // Remainder with unroll
        let mut j = i;
        while j + 4 <= len {
            let d1 = f16::from_bits(*x.add(j)).to_f32() - f16::from_bits(*y.add(j)).to_f32();
            total_sum += d1 * d1;
            let d2 =
                f16::from_bits(*x.add(j + 1)).to_f32() - f16::from_bits(*y.add(j + 1)).to_f32();
            total_sum += d2 * d2;
            let d3 =
                f16::from_bits(*x.add(j + 2)).to_f32() - f16::from_bits(*y.add(j + 2)).to_f32();
            total_sum += d3 * d3;
            let d4 =
                f16::from_bits(*x.add(j + 3)).to_f32() - f16::from_bits(*y.add(j + 3)).to_f32();
            total_sum += d4 * d4;
            j += 4;
        }
        while j < len {
            let diff = f16::from_bits(*x.add(j)).to_f32() - f16::from_bits(*y.add(j)).to_f32();
            total_sum += diff * diff;
            j += 1;
        }
        total_sum
    }

    #[target_feature(enable = "avx2")]
    pub(super) unsafe fn dot_product_i8_avx2(x: *const i8, y: *const i8, len: usize) -> i32 {
        let mut sum_vec = _mm256_setzero_si256();
        let mut i = 0;
        while i + 32 <= len {
            let xv = _mm256_loadu_si256(x.add(i) as *const __m256i);
            let yv = _mm256_loadu_si256(y.add(i) as *const __m256i);

            let x_lo = _mm256_castsi256_si128(xv);
            let y_lo = _mm256_castsi256_si128(yv);
            let x_lo16 = _mm256_cvtepi8_epi16(x_lo);
            let y_lo16 = _mm256_cvtepi8_epi16(y_lo);
            let prod_lo = _mm256_madd_epi16(x_lo16, y_lo16);

            let x_hi = _mm256_extracti128_si256(xv, 1);
            let y_hi = _mm256_extracti128_si256(yv, 1);
            let x_hi16 = _mm256_cvtepi8_epi16(x_hi);
            let y_hi16 = _mm256_cvtepi8_epi16(y_hi);
            let prod_hi = _mm256_madd_epi16(x_hi16, y_hi16);

            sum_vec = _mm256_add_epi32(sum_vec, prod_lo);
            sum_vec = _mm256_add_epi32(sum_vec, prod_hi);
            i += 32;
        }

        // Better reduction for int
        let hi128 = _mm256_extracti128_si256(sum_vec, 1);
        let lo128 = _mm256_castsi256_si128(sum_vec);
        let sum128 = _mm_add_epi32(lo128, hi128);
        let hi64 = _mm_extract_epi64(sum128, 1);
        let lo64 = _mm_extract_epi64(sum128, 0);
        let mut total_sum = (hi64 + lo64) as i32;

        // Unrolled remainder
        let mut j = i;
        while j + 4 <= len {
            total_sum += (*x.add(j) as i32) * (*y.add(j) as i32);
            total_sum += (*x.add(j + 1) as i32) * (*y.add(j + 1) as i32);
            total_sum += (*x.add(j + 2) as i32) * (*y.add(j + 2) as i32);
            total_sum += (*x.add(j + 3) as i32) * (*y.add(j + 3) as i32);
            j += 4;
        }
        while j < len {
            total_sum += (*x.add(j) as i32) * (*y.add(j) as i32);
            j += 1;
        }
        total_sum
    }
}

// --- Modulo per l'implementazione specifica di aarch64 (NEON) ---
#[cfg(target_arch = "aarch64")]
mod aarch64_impl {
    use super::*;
    use std::arch::aarch64::*;

    // Incolla qui TUTTE le tue funzioni SIMD per aarch64 (NEON) che sono STABILI
    #[target_feature(enable = "neon")]
    pub(super) unsafe fn squared_euclidean_f32_neon(
        x: *const f32,
        y: *const f32,
        len: usize,
    ) -> f32 {
        let mut sum_vec = vdupq_n_f32(0.0);
        let mut i = 0;
        while i + 4 <= len {
            let x_vec = vld1q_f32(x.add(i));
            let y_vec = vld1q_f32(y.add(i));
            let diff = vsubq_f32(x_vec, y_vec);
            sum_vec = vfmaq_f32(sum_vec, diff, diff);
            i += 4;
        }

        let mut total_sum = vaddvq_f32(sum_vec);

        let mut j = i;
        while j + 4 <= len {
            let d1 = *x.add(j) - *y.add(j);
            total_sum += d1 * d1;
            let d2 = *x.add(j + 1) - *y.add(j + 1);
            total_sum += d2 * d2;
            let d3 = *x.add(j + 2) - *y.add(j + 2);
            total_sum += d3 * d3;
            let d4 = *x.add(j + 3) - *y.add(j + 3);
            total_sum += d4 * d4;
            j += 4;
        }
        while j < len {
            let diff = *x.add(j) - *y.add(j);
            total_sum += diff * diff;
            j += 1;
        }
        total_sum
    }

    #[target_feature(enable = "neon")]
    pub(super) unsafe fn dot_product_f32_neon(x: *const f32, y: *const f32, len: usize) -> f32 {
        let mut sum_vec = vdupq_n_f32(0.0);
        let mut i = 0;
        while i + 4 <= len {
            let x_vec = vld1q_f32(x.add(i));
            let y_vec = vld1q_f32(y.add(i));
            sum_vec = vfmaq_f32(sum_vec, x_vec, y_vec);
            i += 4;
        }

        let mut total_sum = vaddvq_f32(sum_vec);

        let mut j = i;
        while j + 4 <= len {
            total_sum += *x.add(j) * *y.add(j);
            total_sum += *x.add(j + 1) * *y.add(j + 1);
            total_sum += *x.add(j + 2) * *y.add(j + 2);
            total_sum += *x.add(j + 3) * *y.add(j + 3);
            j += 4;
        }
        while j < len {
            total_sum += *x.add(j) * *y.add(j);
            j += 1;
        }
        total_sum
    }

    #[target_feature(enable = "neon")]
    pub(super) unsafe fn dot_product_i8_neon(x: *const i8, y: *const i8, len: usize) -> i32 {
        let mut sum = vdupq_n_s32(0);
        let mut i = 0;
        while i + 16 <= len {
            let xv = vld1q_s8(x.add(i));
            let yv = vld1q_s8(y.add(i));

            let x_low = vget_low_s8(xv);
            let y_low = vget_low_s8(yv);
            let x_high = vget_high_s8(xv);
            let y_high = vget_high_s8(yv);

            let x_low16 = vmovl_s8(x_low);
            let y_low16 = vmovl_s8(y_low);
            let x_high16 = vmovl_s8(x_high);
            let y_high16 = vmovl_s8(y_high);

            sum = vmlal_s16(sum, vget_low_s16(x_low16), vget_low_s16(y_low16));
            sum = vmlal_s16(sum, vget_high_s16(x_low16), vget_high_s16(y_low16));
            sum = vmlal_s16(sum, vget_low_s16(x_high16), vget_low_s16(y_high16));
            sum = vmlal_s16(sum, vget_high_s16(x_high16), vget_high_s16(y_high16));

            i += 16;
        }

        let mut total_sum: i32 = vaddvq_s32(sum);

        let mut j = i;
        while j + 4 <= len {
            total_sum += (*x.add(j) as i32) * (*y.add(j) as i32);
            total_sum += (*x.add(j + 1) as i32) * (*y.add(j + 1) as i32);
            total_sum += (*x.add(j + 2) as i32) * (*y.add(j + 2) as i32);
            total_sum += (*x.add(j + 3) as i32) * (*y.add(j + 3) as i32);
            j += 4;
        }
        while j < len {
            total_sum += (*x.add(j) as i32) * (*y.add(j) as i32);
            j += 1;
        }
        total_sum
    }

    // NO squared_euclidean_f16_neon perché usa feature instabili
}

// --- Funzioni di Fallback (generiche, compilate sempre) ---
unsafe fn squared_euclidean_f32_fallback(x: *const f32, y: *const f32, len: usize) -> f32 {
    let mut sum: f32 = 0.0;
    for i in 0..len {
        let diff = *x.add(i) - *y.add(i);
        sum += diff * diff;
    }
    sum
}

unsafe fn dot_product_f32_fallback(x: *const f32, y: *const f32, len: usize) -> f32 {
    let mut sum: f32 = 0.0;
    for i in 0..len {
        sum += *x.add(i) * *y.add(i);
    }
    sum
}

unsafe fn squared_euclidean_f16_fallback(x: *const u16, y: *const u16, len: usize) -> f32 {
    let mut sum: f32 = 0.0;
    for i in 0..len {
        let diff = f16::from_bits(*x.add(i)).to_f32() - f16::from_bits(*y.add(i)).to_f32();
        sum += diff * diff;
    }
    sum
}

unsafe fn dot_product_i8_fallback(x: *const i8, y: *const i8, len: usize) -> i32 {
    let mut sum: i64 = 0;
    for i in 0..len {
        sum += (*x.add(i) as i64) * (*y.add(i) as i64);
    }
    sum as i32
}
// (Assicurati di incollare qui il corpo completo di queste 4 funzioni)

// --- Funzioni Pubbliche Esportate (Punto di Ingresso con Dispatch Intelligente) ---

#[unsafe(no_mangle)]
pub unsafe extern "C" fn squared_euclidean_f32(x: *const f32, y: *const f32, len: usize) -> f32 {
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("fma") {
            return x86_64_impl::squared_euclidean_f32_fma(x, y, len);
        }
    }
    #[cfg(target_arch = "aarch64")]
    {
        if is_aarch64_feature_detected!("neon") {
            return aarch64_impl::squared_euclidean_f32_neon(x, y, len);
        }
    }
    squared_euclidean_f32_fallback(x, y, len)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn dot_product_f32(x: *const f32, y: *const f32, len: usize) -> f32 {
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("fma") {
            return x86_64_impl::dot_product_f32_fma(x, y, len);
        }
    }
    #[cfg(target_arch = "aarch64")]
    {
        if is_aarch64_feature_detected!("neon") {
            return aarch64_impl::dot_product_f32_neon(x, y, len);
        }
    }
    dot_product_f32_fallback(x, y, len)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn squared_euclidean_f16(x: *const u16, y: *const u16, len: usize) -> f32 {
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("fma") && is_x86_feature_detected!("f16c") {
            return x86_64_impl::squared_euclidean_f16_fma(x, y, len);
        }
    }
    // Nessuna implementazione NEON stabile per f16, quindi per aarch64 si userà il fallback.
    squared_euclidean_f16_fallback(x, y, len)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn dot_product_i8(x: *const i8, y: *const i8, len: usize) -> i32 {
    #[cfg(target_arch = "x86_64")]
    {
        if is_x86_feature_detected!("avx2") {
            return x86_64_impl::dot_product_i8_avx2(x, y, len);
        }
    }
    #[cfg(target_arch = "aarch64")]
    {
        if is_aarch64_feature_detected!("neon") {
            return aarch64_impl::dot_product_i8_neon(x, y, len);
        }
    }
    dot_product_i8_fallback(x, y, len)
}

// =======================================================================
// === Test Unitari (esegui con cargo test) ===
// =======================================================================

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_squared_euclidean_f32() {
        let v1 = vec![1.0f32, 2.0];
        let v2 = vec![3.0f32, 4.0];
        let res = squared_euclidean_f32(v1.as_ptr(), v2.as_ptr(), 2);
        assert_eq!(res, 8.0);
    }

    #[test]
    fn test_dot_product_f32() {
        let v1 = vec![1.0f32, 2.0, 3.0];
        let v2 = vec![1.0f32, 2.0, 3.0];
        let res = dot_product_f32(v1.as_ptr(), v2.as_ptr(), 3);
        assert_eq!(res, 14.0);
    }

    #[test]
    fn test_squared_euclidean_f16() {
        let v1: Vec<u16> = vec![f16::from_f32(1.0).to_bits(), f16::from_f32(2.0).to_bits()];
        let v2: Vec<u16> = vec![f16::from_f32(3.0).to_bits(), f16::from_f32(4.0).to_bits()];
        let res = squared_euclidean_f16(v1.as_ptr(), v2.as_ptr(), 2);
        assert_eq!(res, 8.0);
    }

    #[test]
    fn test_dot_product_i8() {
        let v1: Vec<i8> = vec![10, 20];
        let v2: Vec<i8> = vec![2, 3];
        let res = dot_product_i8(v1.as_ptr(), v2.as_ptr(), 2);
        assert_eq!(res, 80);

        let v1: Vec<i8> = vec![-1, -2];
        let v2: Vec<i8> = vec![-1, -2];
        let res = dot_product_i8(v1.as_ptr(), v2.as_ptr(), 2);
        assert_eq!(res, 5);
    }
}
