#include "textflag.h"

// func sgemmKernel4x16(a *float32, lda int, b *float32, ldb int, c *float32, ldc int, k int)
//
// C[0:4][0:16] += A[0:4][0:k] × B[0:k][0:16]
// The 4×16 block of C lives in 16 NEON registers (V0-V15) for the whole k loop. Each
// step loads one 16-wide row of B (V16-V19), broadcasts one A value per row (V20-V23)
// and issues 16 fused multiply-adds.
TEXT ·sgemmKernel4x16(SB), NOSPLIT, $0-56
	MOVD a+0(FP), R0
	MOVD lda+8(FP), R1
	LSL  $2, R1, R1
	MOVD b+16(FP), R2
	MOVD ldb+24(FP), R3
	LSL  $2, R3, R3
	MOVD c+32(FP), R4
	MOVD ldc+40(FP), R5
	LSL  $2, R5, R5
	MOVD k+48(FP), R6

	ADD R1, R0, R7  // row 1 of A
	ADD R1, R7, R8  // row 2
	ADD R1, R8, R9  // row 3

	// load C
	MOVD R4, R10
	VLD1 (R10), [V0.S4, V1.S4, V2.S4, V3.S4]
	ADD  R5, R10, R10
	VLD1 (R10), [V4.S4, V5.S4, V6.S4, V7.S4]
	ADD  R5, R10, R10
	VLD1 (R10), [V8.S4, V9.S4, V10.S4, V11.S4]
	ADD  R5, R10, R10
	VLD1 (R10), [V12.S4, V13.S4, V14.S4, V15.S4]

	CBZ R6, store

loop:
	VLD1   (R2), [V16.S4, V17.S4, V18.S4, V19.S4]
	ADD    R3, R2, R2
	VLD1R.P 4(R0), [V20.S4]
	VLD1R.P 4(R7), [V21.S4]
	VLD1R.P 4(R8), [V22.S4]
	VLD1R.P 4(R9), [V23.S4]
	VFMLA  V16.S4, V20.S4, V0.S4
	VFMLA  V17.S4, V20.S4, V1.S4
	VFMLA  V18.S4, V20.S4, V2.S4
	VFMLA  V19.S4, V20.S4, V3.S4
	VFMLA  V16.S4, V21.S4, V4.S4
	VFMLA  V17.S4, V21.S4, V5.S4
	VFMLA  V18.S4, V21.S4, V6.S4
	VFMLA  V19.S4, V21.S4, V7.S4
	VFMLA  V16.S4, V22.S4, V8.S4
	VFMLA  V17.S4, V22.S4, V9.S4
	VFMLA  V18.S4, V22.S4, V10.S4
	VFMLA  V19.S4, V22.S4, V11.S4
	VFMLA  V16.S4, V23.S4, V12.S4
	VFMLA  V17.S4, V23.S4, V13.S4
	VFMLA  V18.S4, V23.S4, V14.S4
	VFMLA  V19.S4, V23.S4, V15.S4
	SUBS   $1, R6, R6
	BNE    loop

store:
	MOVD R4, R10
	VST1 [V0.S4, V1.S4, V2.S4, V3.S4], (R10)
	ADD  R5, R10, R10
	VST1 [V4.S4, V5.S4, V6.S4, V7.S4], (R10)
	ADD  R5, R10, R10
	VST1 [V8.S4, V9.S4, V10.S4, V11.S4], (R10)
	ADD  R5, R10, R10
	VST1 [V12.S4, V13.S4, V14.S4, V15.S4], (R10)
	RET
