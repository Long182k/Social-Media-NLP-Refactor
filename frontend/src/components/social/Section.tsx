import type { ReactNode } from 'react'

export function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
        {action}
      </div>
      {children}
    </section>
  )
}

export function RequestError({ error }: { error: unknown }) {
  if (!error) return null
  return <p className="text-sm text-destructive" role="alert">{error instanceof Error ? error.message : 'Request failed.'}</p>
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="rounded-lg border border-dashed border-border p-5 text-sm text-muted-foreground">{children}</p>
}
