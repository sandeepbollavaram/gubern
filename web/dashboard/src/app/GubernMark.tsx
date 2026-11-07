// Gubern's mark: a ship's wheel abstracted to a hexagon with a helm notch
// ("gubernare", to steer). An original mark, drawn in the brand blue.
export function GubernMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <path d="M16 2.5 27.7 9.25v13.5L16 29.5 4.3 22.75V9.25Z" fill="none" stroke="hsl(var(--primary))" strokeWidth="2.4" strokeLinejoin="round" />
      <path d="M21.5 13.2a6 6 0 1 0 .3 5.2H16.5" fill="none" stroke="hsl(var(--primary))" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="16" cy="16" r="1.6" fill="hsl(var(--primary))" />
    </svg>
  );
}
