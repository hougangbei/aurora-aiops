type BrandLogoProps = {
  size?: number;
  className?: string;
};

export function BrandLogo({
  size = 44,
  className = '',
}: BrandLogoProps) {
  return (
    <div
      role="img"
      aria-label="AIOps 平台 logo"
      className={[
        'relative inline-flex shrink-0 items-center justify-center overflow-hidden rounded-[12px]',
        'border border-indigo-400/40 bg-gradient-to-br from-indigo-600 via-violet-600 to-purple-700',
        'shadow-[0_10px_24px_rgba(79,70,229,0.28)]',
        className,
      ].join(' ')}
      style={{ width: size, height: size }}
    >
      <span
        aria-hidden
        className="relative z-10 text-[21px] font-extrabold leading-none tracking-[-0.08em] text-white"
      >
        A
      </span>
      <span
        aria-hidden
        className="absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-cyan-300 shadow-[0_0_0_3px_rgba(103,232,249,0.16)]"
      />
      <span
        aria-hidden
        className="absolute bottom-1.5 left-1.5 h-1.5 w-1.5 rounded-full bg-violet-200 shadow-[0_0_0_3px_rgba(221,214,254,0.14)]"
      />
    </div>
  );
}
