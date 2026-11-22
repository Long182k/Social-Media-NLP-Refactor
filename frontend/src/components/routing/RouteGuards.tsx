import { useState } from 'react'
import { Link, Navigate, Outlet } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { refreshAccessToken } from '@/lib/api'
import { useUIStore } from '@/stores/uiStore'

function AuthLoadingScreen() {
  return (
    <div
      className="flex min-h-dvh items-center justify-center bg-background p-6"
      role="status"
      aria-live="polite"
    >
      <div className="flex flex-col items-center gap-3 text-muted-foreground">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        <p className="text-sm font-medium">Restoring session...</p>
      </div>
    </div>
  )
}

function SessionRestoreRecovery() {
  const [retrying, setRetrying] = useState(false)
  const clearSession = useUIStore((s) => s.clearSession)

  async function handleRetry() {
    setRetrying(true)
    try {
      await refreshAccessToken()
    } finally {
      setRetrying(false)
    }
  }

  return (
    <div
      className="flex min-h-dvh items-center justify-center bg-background p-6"
      data-testid="session-restore-recovery"
    >
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Temporarily unable to restore session</CardTitle>
          <CardDescription>
            The authentication service is temporarily unavailable. Your session cookie has been preserved.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex items-center gap-3">
          <Button type="button" onClick={handleRetry} disabled={retrying}>
            {retrying ? 'Retrying...' : 'Retry session restore'}
          </Button>
          <Link
            to="/login"
            onClick={() => clearSession()}
            className="text-sm font-medium text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          >
            Sign in instead
          </Link>
        </CardContent>
      </Card>
    </div>
  )
}

export function ProtectedRoute() {
  const authReady = useUIStore((s) => s.authReady)
  const accessToken = useUIStore((s) => s.accessToken)
  const user = useUIStore((s) => s.user)
  const sessionRestoreError = useUIStore((s) => s.sessionRestoreError)

  if (!authReady) {
    return <AuthLoadingScreen />
  }

  if (!accessToken || !user) {
    if (sessionRestoreError) {
      return <SessionRestoreRecovery />
    }
    return <Navigate to="/login" replace />
  }

  return <Outlet />
}

export function PublicOnlyRoute() {
  const authReady = useUIStore((s) => s.authReady)
  const accessToken = useUIStore((s) => s.accessToken)
  const user = useUIStore((s) => s.user)

  if (!authReady) {
    return <AuthLoadingScreen />
  }

  if (accessToken && user) {
    return <Navigate to="/" replace />
  }

  return <Outlet />
}
