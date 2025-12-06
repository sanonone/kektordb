// File: internal/store/distance/gen/main.go
package main

import (
	. "github.com/mmcloughlin/avo/build"
	. "github.com/mmcloughlin/avo/operand"
	reg "github.com/mmcloughlin/avo/reg"
)

func main() {
	// --- Float16 ---
	TEXT("SquaredEuclideanFloat16AVX2", NOSPLIT, "func(v1, v2 []uint16) float32")
	Pragma("noescape")
	Doc("SquaredEuclideanFloat16AVX2 calculates the squared Euclidean distance for float16 vectors (represented as uint16) using AVX2.")
	generateSquaredEuclideanFloat16()
	Generate()
}

func generateSquaredEuclideanFloat16() {
	v1Ptr := Load(Param("v1").Base(), GP64())
	v2Ptr := Load(Param("v2").Base(), GP64())
	n := Load(Param("v1").Len(), GP64())

	sumVec := YMM()
	VXORPS(sumVec, sumVec, sumVec)

	Label("loop_euclidean_f16")
	CMPQ(n, Imm(8))
	JL(LabelRef("remainder_euclidean_f16"))

	v1_f16 := XMM()
	v2_f16 := XMM()
	VMOVDQU(Mem{Base: v1Ptr}, v1_f16)
	VMOVDQU(Mem{Base: v2Ptr}, v2_f16)

	v1_f32 := YMM()
	v2_f32 := YMM()
	VCVTPH2PS(v1_f16, v1_f32)
	VCVTPH2PS(v2_f16, v2_f32)

	diffVec := YMM()
	VSUBPS(v2_f32, v1_f32, diffVec)
	VFMADD231PS(diffVec, diffVec, sumVec)

	ADDQ(Imm(16), v1Ptr)
	ADDQ(Imm(16), v2Ptr)
	SUBQ(Imm(8), n)
	JMP(LabelRef("loop_euclidean_f16"))

	Label("remainder_euclidean_f16")
	CMPQ(n, Imm(0))
	JE(LabelRef("done_euclidean_f16"))

	v1_f16_scalar := XMM()
	v2_f16_scalar := XMM()
	PINSRW(Imm(0), Mem{Base: v1Ptr}, v1_f16_scalar)
	PINSRW(Imm(0), Mem{Base: v2Ptr}, v2_f16_scalar)

	v1_f32_scalar := XMM()
	v2_f32_scalar := XMM()
	VCVTPH2PS(v1_f16_scalar, v1_f32_scalar)
	VCVTPH2PS(v2_f16_scalar, v2_f32_scalar)

	diffScalar := XMM()
	VSUBSS(v2_f32_scalar, v1_f32_scalar, diffScalar)

	sumScalar := XMM()
	VXORPS(sumScalar, sumScalar, sumScalar)
	VFMADD231SS(diffScalar, diffScalar, sumScalar)

	tmp := YMM()
	VMOVDQU(sumScalar.AsY(), tmp)
	VADDPS(tmp, sumVec, sumVec)

	ADDQ(Imm(2), v1Ptr)
	ADDQ(Imm(2), v2Ptr)
	SUBQ(Imm(1), n)
	JMP(LabelRef("remainder_euclidean_f16"))

	Label("done_euclidean_f16")
	sumHorizontal(sumVec)

	ret := XMM()
	VMOVAPS(sumVec.AsX(), ret)
	Store(ret, ReturnIndex(0))
	RET()
}

// sumHorizontal horizontally sums the 8 float32 values in a YMM register.
// The signature uses the TYPE 'reg.YMMRegister'.
func sumHorizontal(vec reg.Register) {
	// The YMM() FUNCTIONS come from 'build'.
	h1 := YMM()
	VEXTRACTF128(Imm(1), vec, h1.AsX())
	VADDPS(vec, h1, vec)

	h2 := YMM()
	VSHUFPS(Imm(0b11101110), vec, vec, h2)
	VADDPS(h2, vec, vec)

	h3 := YMM()
	VSHUFPS(Imm(0b01010101), vec, vec, h3)
	VADDPS(h3, vec, vec)
}
