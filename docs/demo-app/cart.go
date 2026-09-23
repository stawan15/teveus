package demo

// Total returns the price of all items after the discount (0.1 = 10% off).
func Total(prices []float64, discount float64) float64 {
	sum := 0.0
	for _, p := range prices {
		sum += p
	}
	return sum * discount
}
