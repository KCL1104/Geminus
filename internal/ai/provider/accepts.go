package provider

// AcceptsImages reports whether the client hands a prompt's images to its
// backend (an ImageInput that accepts them, T-055), so a caller can tell
// before it spends on an image's other calls: T-056's photo pipeline asks
// the segmenter first, and must not pay for segmentation the proposal call
// would then refuse. The call itself still checks (call).
func (c *Client) AcceptsImages() bool {
	ii, ok := c.backend.(ImageInput)
	return ok && ii.AcceptsImages()
}
