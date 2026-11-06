package model

import "math"

func expf(x float32) float32    { return float32(math.Exp(float64(x))) }
func sqrtf(x float32) float32   { return float32(math.Sqrt(float64(x))) }
func cosf(x float32) float32    { return float32(math.Cos(float64(x))) }
func sinf(x float32) float32    { return float32(math.Sin(float64(x))) }
