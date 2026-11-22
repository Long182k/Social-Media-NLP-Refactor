import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { MessageSquareHeart, Moon, Sun } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { authApi } from '@/lib/authApi'
import { useUIStore } from '@/stores/uiStore'

export default function ResetPasswordPage() {
  const [searchParams] = useSearchParams()
  const theme = useUIStore((s) => s.theme)
  const toggleTheme = useUIStore((s) => s.toggleTheme)

  const [token, setToken] = useState(() => searchParams.get('token') ?? '')
  const [newPassword, setNewPassword] = useState('')
  const [confirmNewPassword, setConfirmNewPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!token.trim()) {
      setError('A password reset token is required.')
      return
    }
    if (newPassword.length < 6) {
      setError('New password must be at least 6 characters.')
      return
    }
    if (newPassword !== confirmNewPassword) {
      setError('Passwords do not match.')
      return
    }

    setIsSubmitting(true)
    setError(null)
    setSuccess(null)
    try {
      const resp = await authApi.resetPassword({
        token: token.trim(),
        newPassword,
      })
      setSuccess(resp.message)
      setNewPassword('')
      setConfirmNewPassword('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to reset password.')
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <div className="relative flex min-h-dvh flex-col items-center justify-center bg-background px-4 py-10">
      <div className="absolute right-4 top-4">
        <Button
          type="button"
          variant="outline"
          size="icon"
          onClick={toggleTheme}
          aria-label="Toggle theme"
        >
          {theme === 'dark' ? (
            <Sun className="h-4 w-4" aria-hidden="true" />
          ) : (
            <Moon className="h-4 w-4" aria-hidden="true" />
          )}
        </Button>
      </div>

      <div className="mb-6 flex items-center gap-2.5">
        <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-xs">
          <MessageSquareHeart className="h-5 w-5" aria-hidden="true" />
        </span>
        <div>
          <h1 className="text-xl font-bold tracking-tight text-foreground">Connected</h1>
          <p className="text-xs text-muted-foreground">Account Recovery</p>
        </div>
      </div>

      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Set a new password</CardTitle>
          <CardDescription>
            Confirm your reset token and choose a new password for your account.
          </CardDescription>
        </CardHeader>

        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4" noValidate>
            <div className="space-y-1.5">
              <label htmlFor="reset-token" className="text-sm font-medium">
                Reset token
              </label>
              <Input
                id="reset-token"
                name="token"
                placeholder="Paste your reset token"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                disabled={isSubmitting}
                required
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="reset-new-password" className="text-sm font-medium">
                New password
              </label>
              <Input
                id="reset-new-password"
                name="newPassword"
                type="password"
                autoComplete="new-password"
                placeholder="At least 6 characters"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                disabled={isSubmitting}
                required
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="reset-confirm-password" className="text-sm font-medium">
                Confirm new password
              </label>
              <Input
                id="reset-confirm-password"
                name="confirmNewPassword"
                type="password"
                autoComplete="new-password"
                placeholder="Re-enter your new password"
                value={confirmNewPassword}
                onChange={(e) => setConfirmNewPassword(e.target.value)}
                disabled={isSubmitting}
                required
              />
            </div>

            {error && (
              <div
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {error}
              </div>
            )}

            {success && (
              <div
                role="status"
                className="rounded-md border border-emerald-300 bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:border-emerald-700/60 dark:bg-emerald-950/60 dark:text-emerald-300"
              >
                {success}. You can now{' '}
                <Link to="/login" className="font-semibold underline">
                  sign in
                </Link>
                .
              </div>
            )}

            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Resetting password...' : 'Reset password'}
            </Button>
          </form>
        </CardContent>

        <CardFooter className="justify-center border-t border-border/70 pt-4 text-sm text-muted-foreground">
          <Link to="/login" className="font-medium text-primary hover:underline">
            Back to sign in
          </Link>
        </CardFooter>
      </Card>
    </div>
  )
}
