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
import { Input } from '@/components/ui/input'
import { authApi } from '@/lib/authApi'
import { useUIStore } from '@/stores/uiStore'

export default function RegisterPage() {
  const navigate = useNavigate()
  const setSession = useUIStore((s) => s.setSession)
  const theme = useUIStore((s) => s.theme)
  const toggleTheme = useUIStore((s) => s.toggleTheme)

  const [username, setUsername] = useState('')
  const [nickName, setNickName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)

  async function handleRegister(e: React.FormEvent) {
    e.preventDefault()
    const cleanUser = username.trim()
    const cleanEmail = email.trim()

    if (cleanUser.length < 3 || cleanUser.length > 50 || /\s/.test(cleanUser)) {
      setError('Username must be 3–50 characters with no spaces.')
      return
    }
    if (!cleanEmail.includes('@')) {
      setError('Please enter a valid email address.')
      return
    }
    if (password.length < 6) {
      setError('Password must be at least 6 characters.')
      return
    }
    if (password !== confirmPassword) {
      setError('Passwords do not match.')
      return
    }

    setIsSubmitting(true)
    setError(null)
    try {
      const resp = await authApi.register({
        username: cleanUser,
        nickName: nickName.trim() || undefined,
        email: cleanEmail,
        password,
      })
      setSession(resp.accessToken, resp.user)
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Registration failed.')
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
          <p className="text-xs text-muted-foreground">Bilingual Social Sentiment Platform</p>
        </div>
      </div>

      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Create an account</CardTitle>
          <CardDescription>
            Join the conversation with real-time Vietnamese &amp; English sentiment insight.
          </CardDescription>
        </CardHeader>

        <CardContent>
          <form onSubmit={handleRegister} className="space-y-4" noValidate>
            <div className="space-y-1.5">
              <label htmlFor="register-username" className="text-sm font-medium">
                Username
              </label>
              <Input
                id="register-username"
                name="username"
                autoComplete="username"
                placeholder="jane_doe"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={isSubmitting}
                required
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="register-nickname" className="text-sm font-medium">
                Display name <span className="text-muted-foreground">(optional)</span>
              </label>
              <Input
                id="register-nickname"
                name="nickName"
                autoComplete="name"
                placeholder="Jane Doe"
                value={nickName}
                onChange={(e) => setNickName(e.target.value)}
                disabled={isSubmitting}
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="register-email" className="text-sm font-medium">
                Email
              </label>
              <Input
                id="register-email"
                name="email"
                type="email"
                autoComplete="email"
                placeholder="jane@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                disabled={isSubmitting}
                required
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="register-password" className="text-sm font-medium">
                Password
              </label>
              <div className="relative">
                <Input
                  id="register-password"
                  name="password"
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="new-password"
                  placeholder="At least 6 characters"
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

            <div className="space-y-1.5">
              <label htmlFor="register-confirm-password" className="text-sm font-medium">
                Confirm password
              </label>
              <Input
                id="register-confirm-password"
                name="confirmPassword"
                type={showPassword ? 'text' : 'password'}
                autoComplete="new-password"
                placeholder="Re-enter your password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
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

            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Creating account...' : 'Create account'}
            </Button>
          </form>
        </CardContent>

        <CardFooter className="justify-center border-t border-border/70 pt-4 text-sm text-muted-foreground">
          <span>
            Already have an account?{' '}
            <Link to="/login" className="font-medium text-primary hover:underline">
              Sign in
            </Link>
          </span>
        </CardFooter>
      </Card>
    </div>
  )
}
