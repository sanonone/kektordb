// File: internal/store/distance_amd64.s
//go:build !noasm && !appengine && !js

#include "textflag.h"

// func squaredEuclideanDistanceAVX2(x, y []float32) (dist float64, err error)
TEXT ·squaredEuclideanDistanceAVX2(SB), NOSPLIT, $0-72
    // param layout (da FP):
    // x_base+0(FP)  -> SI
    // x_len+8(FP)   -> AX
    // y_base+24(FP) -> DI
    // y_len+32(FP)  -> R8
    MOVQ x_base+0(FP), SI
    MOVQ x_len+8(FP), AX
    MOVQ y_base+24(FP), DI
    MOVQ y_len+32(FP), R8

    // verifica lunghezze
    CMPQ AX, R8
    JNE len_mismatch

    // se len == 0 ritorna 0
    TESTQ AX, AX
    JZ len_zero

    // zeroing dei registri YMM/XMM che useremo
    VXORPS Y0, Y0, Y0        // accum0
    VXORPS Y1, Y1, Y1        // accum1
    VXORPS Y2, Y2, Y2        // accum2
    VXORPS Y3, Y3, Y3        // accum3

    // calcola n_blocks = len / 32 (32 float per iter)
    MOVQ AX, CX
    ANDQ $~31, CX            // CX = len & ~31  (multipli di 32)
    JZ   remainder           // meno di 32 elementi: salta al resto

loop_unrolled:
    // prefetch per ridurre cache miss (leggero)
    PREFETCHT0 160(SI)
    PREFETCHT0 160(DI)

    // carica 4*8 float (8 elementi per YMM load)
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

    // square
    VMULPS Y4, Y4, Y4
    VMULPS Y5, Y5, Y5
    VMULPS Y6, Y6, Y6
    VMULPS Y7, Y7, Y7

    // accumula
    VADDPS Y4, Y0, Y0
    VADDPS Y5, Y1, Y1
    VADDPS Y6, Y2, Y2
    VADDPS Y7, Y3, Y3

    ADDQ $128, SI            // 32 float * 4 byte = 128 bytes
    ADDQ $128, DI
    SUBQ $32, CX
    JNE loop_unrolled

    // somma gli accumulatori YMM: Y0 += Y1+Y2+Y3
    VADDPS Y1, Y0, Y0
    VADDPS Y2, Y0, Y0
    VADDPS Y3, Y0, Y0

remainder:
    // riduci Y0 (256-bit) a XMM scalar
    // estrai low e high 128, somma e poi orizzontal-add
    VXORPS X0, X0, X0        // X0 = 0 iniziale accumulo
    VEXTRACTF128 $1, Y0, X1  // X1 = upper 128 bits of Y0
    VEXTRACTF128 $0, Y0, X0  // X0 = lower 128 bits of Y0
    VADDPS X1, X0, X0        // X0 = low + high (4 float)
    VHADDPS X0, X0, X0       // pairwise horizontal add -> 2 floats
    VHADDPS X0, X0, X0       // horizontal add -> scalar in X0[0]

    // calcola il resto scalare len % 32
    MOVQ AX, CX
    ANDQ $31, CX             // CX = len % 32
    JZ   done_sum

    // SI e DI ora puntano alla prima parte *non processata* ?
    // ATTENZIONE: nell'unrolled loop abbiamo già avanzato SI/DI di 128 per iter;
    // ma CX è stato costruito da AX & ~31 e poi abbiamo sottratto in loop.
    // Dopo loop: SI/DI puntano al primo elemento non processato (OK).
    // Accumula i rimanenti CX float (CX < 32)
rem_loop:
    // carica scalare x and y
    MOVSS (SI), X2
    MOVSS (DI), X3
    SUBSS X3, X2         // X2 = x - y
    MULSS X2, X2         // X2 = (x-y)^2
    ADDSS X2, X0         // X0 += X2

    ADDQ $4, SI
    ADDQ $4, DI
    DECQ CX
    JNE rem_loop

done_sum:
    // converti il singolo float in double (scalar)
    VCVTSS2SD X0, X0, X0     // X0 (xmm) low-single -> low-double
    // salva il double in dist (offset 48)
    VMOVSD X0, dist+48(FP)

    // err = nil (due a layout Go: due valori a 56 e 64 per l'interfaccia)
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

