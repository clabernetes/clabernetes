// Switch glyph adapted from srl-labs/clab-ui's SvgGenerator:
// https://github.com/srl-labs/clab-ui/blob/main/src/icons/SvgGenerator.ts
export function SwitchIcon({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      className={className}
      fill="none"
      viewBox="0 0 120 120"
      xmlns="http://www.w3.org/2000/svg"
    >
      <rect fill="currentColor" height="120" rx="18" width="120" />
      <g
        fill="none"
        stroke="white"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="4"
      >
        <path d="m91.5 27.3 7.6 7.6c1.3 1.3 1.3 3.1 0 4.3l-7.6 7.7" />
        <path d="m28.5 46.9-7.6-7.6c-1.3-1.3-1.3-3.1 0-4.3l7.6-7.7" />
        <path d="m91.5 73.1 7.6 7.6c1.3 1.3 1.3 3.1 0 4.3l-7.6 7.7" />
        <path d="m28.5 92.7-7.6-7.6c-1.3-1.3-1.3-3.1 0-4.3l7.6-7.7" />
        <path d="M96.6 36.8H67.9l-16 45.9H23.2" />
        <path d="M96.6 82.7H67.9l-16-45.9H23.2" />
      </g>
    </svg>
  );
}
