// File: internal/store/distance/distance_float16_amd64.s
//go:build !noasm && !appengine && !js

#include "textflag.h"

// func squaredEuclideanDistanceAVX2Float16(x, y []uint16) (dist float64, err error)
// Frame: 32 bytes locals, 72 bytes args area
TEXT ·squaredEuclideanDistanceAVX2Float16(SB), NOSPLIT, $32-72
    // param mapping:
    // x_base+0(FP)  -> SI
    // x_len+8(FP)   -> AX
    // y_base+24(FP) -> DI
    // y_len+32(FP)  -> R8
    MOVQ x_base+0(FP), SI
    MOVQ x_len+8(FP), AX
    MOVQ y_base+24(FP), DI
    MOVQ y_len+32(FP), R8

    // lunghezze uguali?
    CMPQ AX, R8
    JNE len_mismatch

    TESTQ AX, AX
    JZ len_zero

    // accumulator YMM (8 float32 lanes)
    VXORPS Y0, Y0, Y0

    // quanti blocchi di 8 half (8 * 2 byte = 16 byte)
    MOVQ AX, CX
    ANDQ $~7, CX        // CX = len & ~7  (numero di elementi processati in blocchi di 8)
    JZ   remainder_proc

main_loop:
    // carica 8 float16 (128 bit) da x e y in XMM regs
    VMOVDQU 0(SI), X1      // 8 * 16-bit in X1 (non allineato OK)
    VMOVDQU 0(DI), X2

    // converti 8 half -> 8 float32 (YMM)
    VCVTPH2PS X1, Y1       // src X1 (packing half), dest Y1 (8 float32)
    VCVTPH2PS X2, Y2

    // diff = x - y  (inplace in Y1)
    VSUBPS Y2, Y1, Y1      // Y1 = Y1 - Y2

    // square
    VMULPS Y1, Y1, Y1      // Y1 = Y1 * Y1

    // accumula in Y0
    VADDPS Y1, Y0, Y0

    ADDQ $16, SI           // avanzamento 8 * 2 bytes = 16
    ADDQ $16, DI
    SUBQ $8, CX            // 8 elementi consumati
    JNE main_loop

remainder_proc:
    // rimangono 'rem' = len % 8 elementi
    MOVQ AX, CX
    ANDQ $7, CX            // CX = rem
    JZ   final_reduce

    // zero-pad i 16 byte dei buffers temporanei sullo stack (2 buffers da 16 byte ciascuno)
    // layout locale (su SP):
    // 0(SP) .. 15(SP)   -> tmpX (8 * uint16)
    // 16(SP) .. 31(SP)  -> tmpY (8 * uint16)
    MOVQ $0, 0(SP)
    MOVQ $0, 8(SP)
    MOVQ $0, 16(SP)
    MOVQ $0, 24(SP)

    // impostiamo puntatori per la copia dei rimanenti halfs
    LEAQ 0(SP), R12       // destX
    LEAQ 16(SP), R13      // destY
    MOVQ SI, R14          // srcX pointer (rimasto)
    MOVQ DI, R15          // srcY pointer

copy_rem_loop:
    MOVWLZX (R14), R9     // carica uint16 da x -> R9 (zero-extend)
    MOVW R9, 0(R12)       // store word in tmpX
    MOVWLZX (R15), R9     // carica uint16 da y
    MOVW R9, 0(R13)       // store word in tmpY

    ADDQ $2, R14
    ADDQ $2, R15
    ADDQ $2, R12
    ADDQ $2, R13

    DECQ CX
    JNE copy_rem_loop

    // ora tmpX/tmpY contengono r halfs (agli inizi) e zeri sul resto:
    VCVTPH2PS 0(SP), Y1   // converti tmpX -> Y1 (8 float32)
    VCVTPH2PS 16(SP), Y2  // converti tmpY -> Y2

    VSUBPS Y2, Y1, Y1     // Y1 = tmpX - tmpY
    VMULPS Y1, Y1, Y1     // Y1 squared
    VADDPS Y1, Y0, Y0     // accumula in Y0

final_reduce:
    // riduci Y0(8 lanes float32) a scalar float32 in X0
    VEXTRACTF128 $1, Y0, X1   // X1 = upper 128
    VEXTRACTF128 $0, Y0, X0   // X0 = lower 128
    VADDPS X1, X0, X0         // X0 = low + high (4 floats)
    VHADDPS X0, X0, X0        // pairwise hadd -> (2 floats)
    VHADDPS X0, X0, X0        // hadd -> scalar in X0[0]

    // converti scalar float32 -> float64 e salva
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

