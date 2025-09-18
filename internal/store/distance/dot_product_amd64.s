// File: internal/store/dot_product_amd64.s
//go:build !noasm && !appengine && !js

#include "textflag.h"

// func dotProductAVX2(x, y []float32) (dot float64, err error)
TEXT ·dotProductAVX2(SB), NOSPLIT, $0-72
    // param mapping (da FP):
    // x_base+0(FP)  -> SI
    // x_len+8(FP)   -> AX
    // y_base+24(FP) -> DI
    // y_len+32(FP)  -> R8
    MOVQ x_base+0(FP), SI
    MOVQ x_len+8(FP), AX
    MOVQ y_base+24(FP), DI
    MOVQ y_len+32(FP), R8

    // controlla lunghezze
    CMPQ AX, R8
    JNE len_mismatch

    TESTQ AX, AX
    JZ len_zero

    // zeroing dei registri YMM che useremo come accumulatori
    VXORPS Y0, Y0, Y0        // accum0
    VXORPS Y1, Y1, Y1        // accum1
    VXORPS Y2, Y2, Y2        // accum2
    VXORPS Y3, Y3, Y3        // accum3

    // calcola blocchi multipli di 32 (4 YMM loads da 8 float ciascuno)
    MOVQ AX, CX
    ANDQ $~31, CX            // CX = (len / 32) * 32
    JZ   remainder           // meno di 32 elementi -> remainder

loop_unrolled:
    // prefetch per miglior locality
    PREFETCHT0 160(SI)
    PREFETCHT0 160(DI)

    // carica 4 * 8 float da x
    VMOVUPS 0(SI), Y4
    VMOVUPS 32(SI), Y5
    VMOVUPS 64(SI), Y6
    VMOVUPS 96(SI), Y7

    // carica 4 * 8 float da y
    VMOVUPS 0(DI), Y8
    VMOVUPS 32(DI), Y9
    VMOVUPS 64(DI), Y10
    VMOVUPS 96(DI), Y11

    // prodotto element-wise: tmp = x * y
    VMULPS Y8, Y4, Y4    // Y4 = Y4 * Y8
    VMULPS Y9, Y5, Y5    // Y5 = Y5 * Y9
    VMULPS Y10, Y6, Y6   // Y6 = Y6 * Y10
    VMULPS Y11, Y7, Y7   // Y7 = Y7 * Y11

    // accumula nei registri separati
    VADDPS Y4, Y0, Y0
    VADDPS Y5, Y1, Y1
    VADDPS Y6, Y2, Y2
    VADDPS Y7, Y3, Y3

    ADDQ $128, SI        // avanzamento: 32 float * 4 byte = 128 bytes
    ADDQ $128, DI
    SUBQ $32, CX
    JNE loop_unrolled

    // riduci gli accumulatori in Y0
    VADDPS Y1, Y0, Y0
    VADDPS Y2, Y0, Y0
    VADDPS Y3, Y0, Y0

remainder:
    // riduzione Y0(256) -> X0 scalar float
    VXORPS X0, X0, X0
    VEXTRACTF128 $1, Y0, X1   // X1 = high 128
    VEXTRACTF128 $0, Y0, X0   // X0 = low 128
    VADDPS X1, X0, X0         // X0 = low + high (4 float)
    VHADDPS X0, X0, X0        // pairwise hadd -> 2 floats
    VHADDPS X0, X0, X0        // hadd -> scalar in X0[0]

    // calcola il resto scalare len % 32
    MOVQ AX, CX
    ANDQ $31, CX
    JZ   done_sum

    // remainder loop scalare (CX < 32)
rem_loop:
    MOVSS (SI), X2
    MOVSS (DI), X3
    MULSS X3, X2       // X2 = x * y
    ADDSS X2, X0       // X0 += X2

    ADDQ $4, SI
    ADDQ $4, DI
    DECQ CX
    JNE rem_loop

done_sum:
    // converti il float a double e salva
    VCVTSS2SD X0, X0, X0
    VMOVSD X0, dot+48(FP)

    // err = nil (due a layout Go: 2 parole per interface)
    MOVQ $0, err+56(FP)
    MOVQ $0, err+64(FP)

    VZEROUPPER
    RET

len_mismatch:
    VZEROUPPER
    MOVQ $0, dot+48(FP)
    MOVQ $0, err+56(FP)
    MOVQ $0, err+64(FP)
    RET

len_zero:
    VZEROUPPER
    MOVQ $0, dot+48(FP)
    MOVQ $0, err+56(FP)
    MOVQ $0, err+64(FP)
    RET

