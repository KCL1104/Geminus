package vision

// priceImagesAt makes the client price the test models at these prices
// per image, until the returned function restores the build's table
// (spend.ImagePriceOf, which prices none yet).
func priceImagesAt(prices map[string]int64) func() {
	old := imagePriceOf
	imagePriceOf = func(model string) (int64, bool) {
		p, ok := prices[model]
		return p, ok
	}
	return func() { imagePriceOf = old }
}
