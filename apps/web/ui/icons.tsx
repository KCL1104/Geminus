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
