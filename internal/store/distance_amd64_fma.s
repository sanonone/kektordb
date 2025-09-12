//go:build !noasm && !appengine && !js

#include "textflag.h"

// func squaredEuclideanDistanceAVX2FMA(x, y []float32) (dist float64, err error)
TEXT ·squaredEuclideanDistanceAVX2FMA(SB), NOSPLIT, $0-72
    MOVQ x_base+0(FP), SI
    MOVQ x_len+8(FP), AX
    MOVQ y_base+24(FP), DI
    MOVQ y_len+32(FP), R8

    CMPQ AX, R8
    JNE len_mismatch

    TESTQ AX, AX
    JZ len_zero

    VXORPS Y0, Y0, Y0        // accum0
    VXORPS Y1, Y1, Y1        // accum1
    VXORPS Y2, Y2, Y2        // accum2
    VXORPS Y3, Y3, Y3        // accum3

    MOVQ AX, CX
    ANDQ $~31, CX            // multipli di 32
    JZ   remainder

loop_unrolled:
    PREFETCHT0 160(SI)
    PREFETCHT0 160(DI)

    VMOVUPS 0(SI), Y4
    VMOVUPS 32(SI), Y5
    VMOVUPS 64(SI), Y6
    VMOVUPS 96(SI), Y7

    VMOVUPS 0(DI), Y8
    VMOVUPS 32(DI), Y9
    VMOVUPS 64(DI), Y10
    VMOVUPS 96(DI), Y11

    // diff = x - y
    VSUBPS Y8, Y4, Y4
    VSUBPS Y9, Y5, Y5
    VSUBPS Y10, Y6, Y6
    VSUBPS Y11, Y7, Y7

    // accum += diff*diff  (FMA)
    VFMADD213PS Y0, Y4, Y4   // Y0 += Y4*Y4
    VFMADD213PS Y1, Y5, Y5   // Y1 += Y5*Y5
    VFMADD213PS Y2, Y6, Y6   // Y2 += Y6*Y6
    VFMADD213PS Y3, Y7, Y7   // Y3 += Y7*Y7

    ADDQ $128, SI
    ADDQ $128, DI
    SUBQ $32, CX
    JNE loop_unrolled

    VADDPS Y1, Y0, Y0
    VADDPS Y2, Y0, Y0
    VADDPS Y3, Y0, Y0

remainder:
    VXORPS X0, X0, X0
    VEXTRACTF128 $1, Y0, X1
    VEXTRACTF128 $0, Y0, X0
    VADDPS X1, X0, X0
    VHADDPS X0, X0, X0
    VHADDPS X0, X0, X0

    MOVQ AX, CX
    ANDQ $31, CX
    JZ   done_sum

rem_loop:
    MOVSS (SI), X2
    MOVSS (DI), X3
    SUBSS X3, X2
    MULSS X2, X2
    ADDSS X2, X0

    ADDQ $4, SI
    ADDQ $4, DI
    DECQ CX
    JNE rem_loop

done_sum:
    VCVTSS2SD X0, X0, X0
    VMOVSD X0, dist+48(FP)

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

