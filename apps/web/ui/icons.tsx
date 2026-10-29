// Small inline icons. Decorative only: the control they sit in carries the name.
const common = { width: 16, height: 16, viewBox: "0 0 16 16", "aria-hidden": true, focusable: false } as const;

export function CheckIcon() {
  return (
    <svg {...common}>
      <path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function DashIcon() {
  return (
    <svg {...common}>
      <path d="M4 8h8" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

export function ChevronDownIcon() {
  return (
    <svg {...common}>
      <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function SortIcon({ direction }: { direction: "ascending" | "descending" | undefined }) {
  return (
    <svg {...common} data-direction={direction ?? "none"}>
      <path d="M5 6.5l3-3 3 3" fill="none" stroke="currentColor" strokeWidth="1.5" opacity={direction === "descending" ? 0.3 : 1} />
      <path d="M5 9.5l3 3 3-3" fill="none" stroke="currentColor" strokeWidth="1.5" opacity={direction === "ascending" ? 0.3 : 1} />
    </svg>
  );
}

// Action icons (H-040): one 16 px set, 1.5 px strokes with round caps and joins, in currentColor (DESIGN.md Icons).
const line = { fill: "none", stroke: "currentColor", strokeWidth: 1.5, strokeLinecap: "round", strokeLinejoin: "round" } as const;

export function PlusIcon() {
  return (
    <svg {...common}>
      <path d="M8 3v10M3 8h10" {...line} />
    </svg>
  );
}

export function FitIcon() {
  return (
    <svg {...common}>
      <path d="M2.5 6V2.5H6M10 2.5h3.5V6M13.5 10v3.5H10M6 13.5H2.5V10" {...line} />
    </svg>
  );
}

export function SendIcon() {
  return (
    <svg {...common}>
      <path d="M2.5 8h9M8 4.5 11.5 8 8 11.5M14 3v10" {...line} />
    </svg>
  );
}

export function CameraIcon() {
  return (
    <svg {...common}>
      <rect x="2" y="4.5" width="12" height="9" rx="1.5" {...line} />
      <circle cx="8" cy="9" r="2.25" {...line} />
      <path d="M5.5 4.5l1-2h3l1 2" {...line} />
    </svg>
  );
}

export function PublishIcon() {
  return (
    <svg {...common}>
      <path d="M8 11V2.5M4.5 6 8 2.5 11.5 6M3 13.5h10" {...line} />
    </svg>
  );
}

export function SaveIcon() {
  return (
    <svg {...common}>
      <path d="M8 2.5V10M4.5 6.5 8 10l3.5-3.5M3 13.5h10" {...line} />
    </svg>
  );
}

export function PlayIcon() {
  return (
    <svg {...common}>
      <path d="M5 3.5v9l7-4.5z" {...line} />
    </svg>
  );
}

export function CloseIcon() {
  return (
    <svg {...common}>
      <path d="M4 4l8 8M12 4l-8 8" {...line} />
    </svg>
  );
}

export function ApproveIcon() {
  return (
    <svg {...common}>
      <path d="M3 8.5l3.25 3.25L13 5" {...line} />
    </svg>
  );
}

export function AskIcon() {
  return (
    <svg {...common}>
      <path d="M2.5 3.5h11v7H8l-3 2.5v-2.5H2.5z" {...line} />
    </svg>
  );
}
