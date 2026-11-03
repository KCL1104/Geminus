// The landing's product screens (H-048): the files under public/landing/ and
// their width and height on screen (1x; each theme's second file is 2x).
// Written by apps/web-e2e/scripts/landing-shots.mjs; run it again instead of
// editing this file.
export type ShotData = { width: number; height: number; themes: { light: string[]; dark?: string[] } };

export const shots = {
  "canvas": {
    "width": 1208,
    "height": 580,
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
  "proposed": {
    "width": 1184,
    "height": 795,
    "themes": {
      "light": [
        "proposed-light-1184.webp",
        "proposed-light-2368.webp"
      ],
      "dark": [
        "proposed-dark-1184.webp",
        "proposed-dark-2368.webp"
      ]
    }
  },
  "results": {
    "width": 1028,
    "height": 348,
    "themes": {
      "light": [
        "results-light-1028.webp",
        "results-light-2056.webp"
      ],
      "dark": [
        "results-dark-1028.webp",
        "results-dark-2056.webp"
      ]
    }
  },
  "marey": {
    "width": 1028,
    "height": 389,
    "themes": {
      "light": [
        "marey-light-1028.webp",
        "marey-light-2056.webp"
      ],
      "dark": [
        "marey-dark-1028.webp",
        "marey-dark-2056.webp"
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
  }
} satisfies Record<string, ShotData>;
