import type { ReactNode } from "react";

/** AuthCard frames the pages one sees signed out. */
export function AuthCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="grid flex-1 place-items-center overflow-y-auto bg-linear-to-b from-neutral-50 to-white px-4 py-16">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center font-bold text-2xl tracking-[-0.04em]">otogi</div>
        <div className="surface-raised rounded-2xl p-6">
          <h1 className="font-semibold text-lg tracking-tight">{title}</h1>
          {children}
        </div>
      </div>
    </main>
  );
}
