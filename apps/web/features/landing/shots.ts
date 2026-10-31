// The landing's product screens (H-048): the files under public/landing/ and
// their width and height on screen (1x; each theme's second file is 2x).
// Written by apps/web-e2e/scripts/landing-shots.mjs; run it again instead of
// editing this file.
export type ShotData = { width: number; height: number; themes: { light: string[]; dark?: string[] } };

export const shots = {
  "canvas": {
    "width": 1208,
    "height": 702,
    "themes": {
      "light": [
        "canvas-light-1208.webp",
        "canvas-light-2416.webp"
      ],
      "dark": [
        "canvas-dark-1208.webp",
        "canvas-dark-2416.webp"
      ]
    }
  },
  "results": {
    "width": 968,
    "height": 348,
    "themes": {
      "light": [
        "results-light-968.webp",
        "results-light-1936.webp"
      ],
      "dark": [
        "results-dark-968.webp",
        "results-dark-1936.webp"
      ]
    }
  },
  "marey": {
    "width": 716,
    "height": 389,
    "themes": {
      "light": [
        "marey-light-716.webp",
        "marey-light-1432.webp"
      ],
      "dark": [
        "marey-dark-716.webp",
        "marey-dark-1432.webp"
      ]
    }
  },
  "explained": {
    "width": 408,
    "height": 748,
    "themes": {
      "light": [
        "explained-408.webp"
      ]
    }
  },
  "proposed": {
    "width": 1188,
    "height": 700,
    "themes": {
      "light": [
        "proposed-1188.webp"
      ]
    }
  }
} satisfies Record<string, ShotData>;
