// File: distance_int8_amd64.s
//go:build !noasm && !appengine && !js

#include "textflag.h"

// func dotProductAVX2Int8(x, y []int8) (dot int32, err error)
TEXT ·dotProductAVX2Int8(SB), NOSPLIT, $0-48
    MOVQ x_base+0(FP), SI
    MOVQ x_len+8(FP), AX
    MOVQ y_base+16(FP), DI
    MOVQ y_len+24(FP), R8

    CMPQ AX, R8
    JNE len_mismatch

    TESTQ AX, AX
    JZ len_zero

    VPXOR Y0, Y0, Y0

    MOVQ AX, CX
    ANDQ $~15, CX
    JZ remainder_only // Se non ci sono blocchi da 16, salta tutta la parte AVX

    // --- Inizio Blocco AVX (len >= 16) ---
main_loop:
    VMOVDQU (SI), X1
    VMOVDQU (DI), X2
    VPMOVSXBW X1, Y3
    VPMOVSXBW X2, Y4
    VPMADDWD Y4, Y3, Y5
    VPADDD Y5, Y0, Y0
    ADDQ $16, SI
    ADDQ $16, DI
    SUBQ $16, CX
    JNE main_loop

    VEXTRACTI128 $1, Y0, X1
    VPADDD X1, X0, X0
    VPHADDD X0, X0, X0
    VPHADDD X0, X0, X0
    VMOVD X0, R9

    // CORREZIONE CRUCIALE: Salta la logica di inizializzazione e vai
    // direttamente al calcolo del resto.
    JMP calculate_remainder
    // --- Fine Blocco AVX ---

remainder_only:
    // Questo blocco viene eseguito SOLO se len < 16.
    // Inizializza l'accumulatore scalare.
    MOVQ $0, R9

calculate_remainder:
    // Entrambi i percorsi (AVX e solo-resto) convergono qui.
    MOVQ AX, CX
    ANDQ $15, CX // Calcola il numero di elementi rimanenti
    JZ finalize

rem_loop:
    // Metodo di estensione del segno manuale a prova di bomba.
    MOVB (SI), R10   // Carica il byte
    SHLQ $56, R10    // Sposta il bit di segno nella posizione più alta
    SARQ $56, R10    // Propaga il bit di segno in tutto il registro

    MOVB (DI), R11
    SHLQ $56, R11
    SARQ $56, R11

    IMULQ R11, R10
    ADDQ R10, R9

    INCQ SI
    INCQ DI
    DECQ CX
    JNE rem_loop

finalize:
    MOVL R9, dot+32(FP)
    MOVQ $0, err+40(FP)
    MOVQ $0, err+48(FP)
    VZEROUPPER
    RET

len_mismatch:
    MOVL $0, dot+32(FP)
    JMP finalize

len_zero:
    MOVL $0, dot+32(FP)
    JMP finalize
