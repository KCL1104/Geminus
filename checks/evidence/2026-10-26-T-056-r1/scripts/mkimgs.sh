#!/usr/bin/env bash
# T-056 review round 1, M2: the measurement images (ImageMagick 6.9.12),
# 4096x4096 (and 2048x2048 in a second directory): the review's four
# expensive kinds and the kinds the fix keeps accepting.
set -eu
out="$1"; side="${2:-4096}"
mkdir -p "$out"
cd "$out"
S="${side}x${side}"
base() { convert -size "$S" gradient:red-blue -swirl 90 "$@"; }
# The review's four (refused after the fix).
base -depth 16 -colorspace gray -type grayscale gray16.png
base -alpha set -channel A -evaluate set 60% +channel -depth 16 rgba16.png
base -sampling-factor 1x1 -interlace JPEG -quality 85 prog444.jpg
base -colorspace CMYK -interlace JPEG -quality 85 progcmyk.jpg
# More the fix refuses.
base -colorspace CMYK -interlace none -quality 85 basecmyk.jpg
base -alpha set -channel A -evaluate set 60% +channel -depth 8 -interlace PNG rgba8-interlaced.png
# The accepted kinds.
base -alpha set -channel A -evaluate set 60% +channel -depth 8 -define png:color-type=6 rgba8.png
base -depth 8 -define png:color-type=2 rgb8.png
base -depth 8 -colorspace gray -type grayscale gray8.png
base -colors 200 -type palette palette8.png
base -sampling-factor 1x1 -interlace none -quality 85 base444.jpg
base -sampling-factor 2x2 -interlace none -quality 85 base420.jpg
base -colorspace gray -interlace none -quality 85 gray8.jpg
ls -l
identify *
