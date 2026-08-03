import { useEffect, useState } from 'react';

type CoreSpinLoaderProps = {
  className?: string;
  minHeight?: string;
};

const loadingStates = [
  'Initializing',
  'Loading...',
  'Fetching Data..',
  'Syncing...',
  'Processing..',
  'Optimizing...',
];

export function CoreSpinLoader({
  className = '',
  minHeight = '220px',
}: CoreSpinLoaderProps) {
  const [loadingText, setLoadingText] = useState(loadingStates[0]);

  useEffect(() => {
    let index = 0;
    const interval = window.setInterval(() => {
      index = (index + 1) % loadingStates.length;
      setLoadingText(loadingStates[index]);
    }, 1000);

    return () => window.clearInterval(interval);
  }, []);

  return (
    <div
      role="status"
      aria-live="polite"
      className={[
        'flex min-h-[200px] flex-col items-center justify-center gap-8',
        className,
      ].join(' ')}
      style={{ minHeight }}
    >
      <div
        className="relative flex h-20 w-20 items-center justify-center"
        aria-hidden="true"
      >
        <div className="absolute inset-0 animate-pulse rounded-full bg-emerald-400/15 blur-xl dark:bg-cyan-500/10" />
        <div className="absolute inset-0 animate-[spin_10s_linear_infinite] rounded-full border border-dashed border-emerald-500/40 dark:border-cyan-500/20" />
        <div className="absolute inset-1 animate-[spin_2s_linear_infinite] rounded-full border-2 border-transparent border-t-emerald-500 shadow-[0_0_6px_rgba(16,185,129,0.5)] dark:border-t-cyan-400 dark:shadow-[0_0_10px_rgba(34,211,238,0.4)]" />
        <div className="absolute inset-3 animate-[spin_3s_linear_infinite_reverse] rounded-full border-2 border-transparent border-b-green-600 shadow-[0_0_6px_rgba(22,163,74,0.4)] dark:border-b-purple-500 dark:shadow-[0_0_10px_rgba(168,85,247,0.4)]" />
        <div className="absolute inset-5 animate-[spin_1s_ease-in-out_infinite] rounded-full border border-transparent border-l-green-700/60 dark:border-l-white/50" />
        <div className="absolute inset-0 animate-[spin_4s_linear_infinite]">
          <div className="absolute left-1/2 top-0 h-1 w-1 -translate-x-1/2 rounded-full bg-emerald-600 shadow-[0_0_4px_rgba(16,185,129,0.9)] dark:bg-cyan-400 dark:shadow-[0_0_6px_rgba(34,211,238,0.8)]" />
        </div>
        <div className="absolute h-2 w-2 animate-pulse rounded-full bg-emerald-700 shadow-[0_0_6px_rgba(16,185,129,0.6)] dark:bg-white dark:shadow-[0_0_10px_rgba(255,255,255,0.8)]" />
      </div>
      <span
        key={loadingText}
        className="h-8 animate-pulse text-[10px] font-medium uppercase tracking-[0.3em] text-emerald-700 dark:text-cyan-200/70"
      >
        {loadingText}
      </span>
    </div>
  );
}
