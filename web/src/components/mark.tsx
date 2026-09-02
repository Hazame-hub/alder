/**
 * AlderMark is the product mark: an alder leaf over the waterline it hardens
 * under, drawn as a single path so it stays crisp at 16px in a browser tab.
 */
export function AlderMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      className={className}
      role="img"
      aria-label="Alder"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
    >
      <rect width="32" height="32" rx="7" className="fill-primary" />
      <path
        d="M16 6c-4.6 2.2-7.4 5.6-7.4 9.4 0 3 1.9 5.4 4.6 6.3l-1.3 3.5h2.2l1-2.8h1.8l1 2.8h2.2l-1.3-3.5c2.7-.9 4.6-3.3 4.6-6.3C23.4 11.6 20.6 8.2 16 6Zm0 3c3 1.8 4.8 4.1 4.8 6.4 0 2.3-1.7 4-4 4.3V11h-1.6v8.7c-2.3-.3-4-2-4-4.3C11.2 13.1 13 10.8 16 9Z"
        className="fill-primary-foreground"
      />
    </svg>
  );
}
