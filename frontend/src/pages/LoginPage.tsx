import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Eye, EyeOff, MessageSquareHeart, Moon, Sun } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api'
import { authApi } from '@/lib/authApi'
import { useUIStore } from '@/stores/uiStore'

export default function LoginPage() {
  const navigate = useNavigate()
  const setSession = useUIStore((s) => s.setSession)
  const theme = useUIStore((s) => s.theme)
  const toggleTheme = useUIStore((s) => s.toggleTheme)

  const [identifier, setIdentifier] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)

  const [forgotOpen, setForgotOpen] = useState(false)
  const [forgotEmail, setForgotEmail] = useState('')
  const [forgotError, setForgotError] = useState<string | null>(null)
  const [forgotSuccess, setForgotSuccess] = useState<string | null>(null)
  const [isSendingReset, setIsSendingReset] = useState(false)

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault()
    if (!identifier.trim() || !password) {
      setError('Please enter your username or email and password.')
      return
    }
    setIsSubmitting(true)
    setError(null)
    try {
      const resp = await authApi.login({
        identifier: identifier.trim(),
        password,
      })
      setSession(resp.accessToken, resp.user)
      navigate('/', { replace: true })
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 429) {
          setError('Too many failed sign-in attempts. Please wait before trying again.')
        } else if (err.status === 403) {
          setError('This account is deactivated. Contact an administrator.')
        } else {
          setError(err.message)
        }
      } else {
        setError('Unable to sign in right now. Please try again.')
      }
    } finally {
      setIsSubmitting(false)
    }
  }

  async function handleForgotPassword(e: React.FormEvent) {
    e.preventDefault()
    if (!forgotEmail.trim()) {
      setForgotError('Please enter your email address.')
      return
    }
    setIsSendingReset(true)
    setForgotError(null)
    setForgotSuccess(null)
    try {
      const resp = await authApi.forgotPassword({ email: forgotEmail.trim() })
      setForgotSuccess(resp.message)
    } catch (err) {
      setForgotError(
        err instanceof Error ? err.message : 'Unable to process password reset request.',
      )
    } finally {
      setIsSendingReset(false)
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
          <p className="text-xs text-muted-foreground">Bilingual Social Sentiment Platform</p>
        </div>
      </div>

      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Sign in to your account</CardTitle>
          <CardDescription>
            Enter your username or email to access the social feed.
          </CardDescription>
        </CardHeader>

        <CardContent>
          <form onSubmit={handleLogin} className="space-y-4" noValidate>
            <div className="space-y-1.5">
              <label htmlFor="login-identifier" className="text-sm font-medium">
                Username or Email
              </label>
              <Input
                id="login-identifier"
                name="identifier"
                type="text"
                autoComplete="username"
                placeholder="alice or alice@example.com"
                value={identifier}
                onChange={(e) => setIdentifier(e.target.value)}
                disabled={isSubmitting}
                required
              />
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <label htmlFor="login-password" className="text-sm font-medium">
                  Password
                </label>
                <button
                  type="button"
                  onClick={() => {
                    setForgotEmail('')
                    setForgotError(null)
                    setForgotSuccess(null)
                    setForgotOpen(true)
                  }}
                  className="text-xs font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  Forgot password?
                </button>
              </div>
              <div className="relative">
                <Input
                  id="login-password"
                  name="password"
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  placeholder="Enter your password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={isSubmitting}
                  className="pr-10"
                  required
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((prev) => !prev)}
                  aria-label={showPassword ? 'Hide password' : 'Show password'}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {showPassword ? (
                    <EyeOff className="h-4 w-4" aria-hidden="true" />
                  ) : (
                    <Eye className="h-4 w-4" aria-hidden="true" />
                  )}
                </button>
              </div>
            </div>

            {error && (
              <div
                role="alert"
                className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {error}
              </div>
            )}

            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Signing in...' : 'Sign in'}
            </Button>
          </form>
        </CardContent>

        <CardFooter className="justify-center border-t border-border/70 pt-4 text-sm text-muted-foreground">
          <span>
            New to Connected?{' '}
            <Link to="/register" className="font-medium text-primary hover:underline">
              Create an account
            </Link>
          </span>
        </CardFooter>
      </Card>

      <Dialog open={forgotOpen} onOpenChange={setForgotOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Reset your password</DialogTitle>
            <DialogDescription>
              Enter the email address associated with your account and we will send reset
              instructions.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={handleForgotPassword} className="mt-4 space-y-4">
            <div className="space-y-1.5">
              <label htmlFor="forgot-email" className="text-sm font-medium">
                Email address
              </label>
              <Input
                id="forgot-email"
                type="email"
                autoComplete="email"
                placeholder="you@example.com"
                value={forgotEmail}
                onChange={(e) => setForgotEmail(e.target.value)}
                disabled={isSendingReset}
                required
              />
            </div>

            {forgotError && (
              <p role="alert" className="text-sm text-destructive">
                {forgotError}
              </p>
            )}
            {forgotSuccess && (
              <p
                role="status"
                className="rounded-md border border-emerald-300 bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:border-emerald-700/60 dark:bg-emerald-950/60 dark:text-emerald-300"
              >
                {forgotSuccess}
              </p>
            )}

            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setForgotOpen(false)}
                disabled={isSendingReset}
              >
                Close
              </Button>
              <Button type="submit" disabled={isSendingReset}>
                {isSendingReset ? 'Sending...' : 'Send reset link'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
