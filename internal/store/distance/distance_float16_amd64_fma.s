// File: internal/store/distance/distance_float16_amd64_fma.s
//go:build !noasm && !appengine && !js

#include "textflag.h"

// func squaredEuclideanDistanceAVX2Float16FMA(x, y []uint16) (dist float64, err error)
// Frame: 32 bytes locals, 72 bytes args area
TEXT ·squaredEuclideanDistanceAVX2Float16FMA(SB), NOSPLIT, $32-72
    // param mapping:
    // x_base+0(FP)  -> SI
    // x_len+8(FP)   -> AX
    // y_base+24(FP) -> DI
    // y_len+32(FP)  -> R8
    MOVQ x_base+0(FP), SI
    MOVQ x_len+8(FP), AX
    MOVQ y_base+24(FP), DI
    MOVQ y_len+32(FP), R8

    // lengths equal?
    CMPQ AX, R8
    JNE len_mismatch

    TESTQ AX, AX
    JZ len_zero

    // accumulator YMM (8 float32 lanes)
    VXORPS Y0, Y0, Y0

    // process blocks of 8 half-precision elements (8 * 2 bytes = 16)
    MOVQ AX, CX
    ANDQ $~7, CX           // CX = len rounded down to multiple of 8
    JZ   remainder_proc

main_loop:
    // load 8 * uint16 into XMM regs (unaligned safe)
    VMOVDQU 0(SI), X1
    VMOVDQU 0(DI), X2

    // half -> float32 (8 lanes)
    VCVTPH2PS X1, Y1       // Y1 = x (float32)
    VCVTPH2PS X2, Y2       // Y2 = y

    // diff = x - y  (in Y1)
    VSUBPS Y2, Y1, Y1      // Y1 = Y1 - Y2

    // accum += diff * diff  (FMA hot path)
    // CORREZIONE: Usa VFMADD231PS per accumulare in Y0. Y0 = (Y1*Y1) + Y0
    VFMADD231PS Y1, Y1, Y0

    ADDQ $16, SI
    ADDQ $16, DI
    SUBQ $8, CX
    JNE main_loop

remainder_proc:
    // rem = len % 8
    MOVQ AX, CX
    ANDQ $7, CX
    JZ   final_reduce

    // zero-pad two 16-byte buffers on stack for tmpX/tmpY
    // stack layout:
    // 0(SP) .. 15(SP)   -> tmpX (8 * uint16)
    // 16(SP) .. 31(SP)  -> tmpY (8 * uint16)
    MOVQ $0, 0(SP)
    MOVQ $0, 8(SP)
    MOVQ $0, 16(SP)
    MOVQ $0, 24(SP)

    // set pointers for copying remainder
    LEAQ 0(SP), R12       // dest tmpX
    LEAQ 16(SP), R13      // dest tmpY
    MOVQ SI, R14          // srcX pointer (current position)
    MOVQ DI, R15          // srcY pointer

copy_rem_loop:
    MOVWLZX (R14), R9     // load uint16 x (zero-extend)
    MOVW R9, 0(R12)       // store word into tmpX
    MOVWLZX (R15), R9     // load uint16 y
    MOVW R9, 0(R13)       // store word into tmpY

    ADDQ $2, R14
    ADDQ $2, R15
    ADDQ $2, R12
    ADDQ $2, R13

    DECQ CX
    JNE copy_rem_loop

    // convert padded halves -> float32 vectors
    VCVTPH2PS 0(SP), Y1    // tmpX -> Y1 (8 float32)
    VCVTPH2PS 16(SP), Y2   // tmpY -> Y2 (8 float32)

    // remainder: diff, square, add
    VSUBPS Y2, Y1, Y1      // Y1 = tmpX - tmpY
    // CORREZIONE: Usa VFMADD231PS per accumulare in Y0. Y0 = (Y1*Y1) + Y0
    VFMADD231PS Y1, Y1, Y0

final_reduce:
    // reduce Y0(8 lanes float32) to scalar float32 in X0
    VEXTRACTF128 $1, Y0, X1   // X1 = upper 128
    VEXTRACTF128 $0, Y0, X0   // X0 = lower 128
    VADDPS X1, X0, X0         // X0 = low + high (4 floats)
    VHADDPS X0, X0, X0        // pairwise hadd -> 2 floats
    VHADDPS X0, X0, X0        // hadd -> scalar in X0[0]

    // convert scalar float32 -> float64 and store
    VCVTSS2SD X0, X0, X0
    VMOVSD X0, dist+48(FP)

    // err = nil
    MOVQ $0, err+56(FP)
    MOVQ $0, err+64(FP)

    VZEROUPPER
    RET

len_mismatch:
    VZEROUPPER
    MOVQ $0, dist+48(FP)
    MOVQ $0, err+56(FP)
    MOVQ $0, err+64(FP)
    RET

len_zero:
    VZEROUPPER
    MOVQ $0, dist+48(FP)
    MOVQ $0, err+56(FP)
    MOVQ $0, err+64(FP)
    RET
