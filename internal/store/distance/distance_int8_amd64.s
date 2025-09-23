// File: distance_int8_amd64.s
//go:build !noasm && !appengine && !js

#include "textflag.h"

// func dotProductAVX2Int8(x, y []int8) (dot int32, err error)
TEXT ·dotProductAVX2Int8(SB), NOSPLIT, $0-48
    // Parametri Go
    MOVQ x_base+0(FP), SI   // SI = &x[0]
    MOVQ x_len+8(FP), AX    // AX = len(x)
    MOVQ y_base+16(FP), DI  // DI = &y[0]
    MOVQ y_len+24(FP), R8   // R8 = len(y)

    // Controlla che le lunghezze coincidano
    CMPQ AX, R8
    JNE len_mismatch

    TESTQ AX, AX
    JZ len_zero

    // Accumulatore vettoriale a zero
    VPXOR Y0, Y0, Y0

    // Processa blocchi di 16 byte
    MOVQ AX, CX
    ANDQ $~15, CX   // CX = multiplo di 16
    JZ remainder

main_loop:
    VMOVDQU (SI), X1   // carica 16 int8 da x
    VMOVDQU (DI), X2   // carica 16 int8 da y

    VPMOVSXBW X1, Y3   // estende 16x int8 -> 16x int16
    VPMOVSXBW X2, Y4   // idem per y

    VPMADDWD Y4, Y3, Y5 // prodotti + add per coppie, 8x int32
    VPADDD   Y5, Y0, Y0 // accumula

    ADDQ $16, SI
    ADDQ $16, DI
    SUBQ $16, CX
    JNE main_loop

    // Riduzione parziale: Y0 (8x int32) -> somma
    VEXTRACTI128 $1, Y0, X1
    VPADDD X1, X0, X0
    VPHADDD X0, X0, X0
    VPHADDD X0, X0, X0

    VMOVD X0, R9       // R9 = somma parziale

remainder:
    // Se nessun blocco da 16, inizializza accumulatore a zero
    TESTQ AX, AX
    JZ remainder_init_done
    CMPQ AX, $16
    JGE remainder_init_done
    MOVQ $0, R9
remainder_init_done:

    // Resto (len % 16)
    MOVQ AX, CX
    ANDQ $15, CX
    JZ finalize

rem_loop:
    MOVBLZX (SI), R10   // carica int8 come uint8
    MOVBLZX (DI), R11

    // estendi con segno da byte a int64
    MOVBLSX R10, R10
    MOVBLSX R11, R11

    IMULQ R11, R10
    ADDQ R10, R9

    INCQ SI
    INCQ DI
    DECQ CX
    JNE rem_loop

finalize:
    MOVL R9, dot+32(FP)   // salva risultato come int32
    MOVQ $0, err+40(FP)
    MOVQ $0, err+48(FP)
    VZEROUPPER
    RET

len_mismatch:
    MOVL $0, dot+32(FP)
    MOVQ $0, err+40(FP)
    MOVQ $0, err+48(FP)
    VZEROUPPER
    RET

len_zero:
    MOVL $0, dot+32(FP)
    MOVQ $0, err+40(FP)
    MOVQ $0, err+48(FP)
    VZEROUPPER
    RET

