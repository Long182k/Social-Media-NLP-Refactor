import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, Bookmark, CalendarDays, House, LogOut, MessageCircle, MessageSquareHeart, Moon, Search, Settings, ShieldCheck, Sun, UserRound, UsersRound, X } from 'lucide-react'
import { Avatar } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { broadcastSessionCleared } from '@/lib/api'
import { authApi } from '@/lib/authApi'
import { unreadNotifications, unreadNotificationsKey } from '@/lib/socialApi'
import { useRealtime } from '@/lib/useRealtime'
import { cn } from '@/lib/utils'
import { useUIStore } from '@/stores/uiStore'

export function AppLayout() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const user = useUIStore((s) => s.user)
  const theme = useUIStore((s) => s.theme)
  const toggleTheme = useUIStore((s) => s.toggleTheme)
  const clearSession = useUIStore((s) => s.clearSession)
  useRealtime(user?.id)
  const unread = useQuery({
    queryKey: [...unreadNotificationsKey, user?.id],
    queryFn: unreadNotifications,
    enabled: !!user,
  }).data?.unreadCount ?? 0
  const home = location.pathname === '/'

  const [searchInput, setSearchInput] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [signingOut, setSigningOut] = useState(false)

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(searchInput.trim())
    }, 200)
    return () => window.clearTimeout(timer)
  }, [searchInput])

  function handleClearSearch() {
    setSearchInput('')
    setDebouncedQuery('')
  }

  async function handleSignOut() {
    setSigningOut(true)
    try {
      await authApi.signout()
    } catch {
      // Clear client state even if network signout fails
    } finally {
      clearSession()
      broadcastSessionCleared()
      queryClient.clear()
      setSigningOut(false)
      navigate('/login', { replace: true })
    }
  }

  const displayName = user?.nickName || user?.userName || 'Member'
  const routes = [
    { path: '/', name: 'Home', icon: House },
    { path: '/bookmarks', name: 'Bookmarks', icon: Bookmark },
    { path: '/groups', name: 'Groups', icon: UsersRound },
    { path: '/events', name: 'Events', icon: CalendarDays },
    { path: '/messages', name: 'Messages', icon: MessageCircle },
    { path: '/notifications', name: 'Notifications', icon: Bell },
    { path: `/profile/${user?.id}`, name: 'Profile', icon: UserRound },
    { path: '/settings', name: 'Settings', icon: Settings },
    ...(user?.role === 'ADMIN' ? [{ path: '/admin', name: 'Admin', icon: ShieldCheck }] : []),
  ]

  return (
    <div className="min-h-dvh bg-background text-foreground">
      <header className="sticky top-0 z-40 border-b border-border bg-card/90 backdrop-blur-md">
        <div className="mx-auto flex max-w-4xl flex-wrap items-center justify-between gap-3 px-4 py-3">
          <Link to="/" className="flex items-center gap-2.5 rounded-md focus-visible:ring-2 focus-visible:ring-ring">
            <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-xs">
              <MessageSquareHeart className="h-5 w-5" aria-hidden="true" />
            </span>
            <div>
              <span className="text-base font-bold tracking-tight text-foreground">
                Connected
              </span>
              <span className="hidden text-xs text-muted-foreground sm:block">
                Social Sentiment Feed
              </span>
            </div>
          </Link>

          {home && <div className="order-3 w-full sm:order-2 sm:max-w-xs sm:flex-1">
            <div className="relative">
              <Search
                className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
                aria-hidden="true"
              />
              <Input
                type="search"
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                placeholder="Search posts..."
                aria-label="Search posts"
                className="h-9 pl-9 pr-8"
              />
              {searchInput && (
                <button
                  type="button"
                  onClick={handleClearSearch}
                  aria-label="Clear search"
                  className="absolute right-2 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <X className="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              )}
            </div>
          </div>}

          <div className="order-2 flex items-center gap-2 sm:order-3">
            <Button
              type="button"
              variant="outline"
              size="icon"
              onClick={toggleTheme}
              aria-label="Toggle theme"
              title={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
            >
              {theme === 'dark' ? (
                <Sun className="h-4 w-4" aria-hidden="true" />
              ) : (
                <Moon className="h-4 w-4" aria-hidden="true" />
              )}
            </Button>

            {user && (
              <div
                className="hidden items-center gap-2 rounded-full border border-border bg-muted/60 py-1 pl-1 pr-3 md:flex"
                data-testid="user-badge"
              >
                <Avatar name={displayName} src={user.avatarUrl} className="h-7 w-7 text-xs" />
                <div className="leading-tight">
                  <p className="max-w-28 truncate text-xs font-semibold text-foreground">
                    {user.nickName}
                  </p>
                  <p className="max-w-28 truncate text-[11px] text-muted-foreground">
                    @{user.userName}
                  </p>
                </div>
              </div>
            )}

            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={handleSignOut}
              disabled={signingOut}
            >
              <LogOut className="h-4 w-4" aria-hidden="true" />
              <span>{signingOut ? 'Signing out...' : 'Sign out'}</span>
            </Button>
          </div>
        </div>
        <nav aria-label="Main navigation" className="mx-auto flex max-w-4xl flex-wrap gap-1 px-4 pb-2">
          {routes.map(({ path, name, icon: Icon }) => (
            <NavLink
              key={path}
              end={path === '/'}
              to={path}
              className={({ isActive }) => cn(
                'inline-flex min-h-9 items-center gap-1.5 rounded-md px-2.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                isActive ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              )}
            >
              <Icon className="h-4 w-4" aria-hidden="true" />
              <span>{name}</span>
              {name === 'Notifications' && unread > 0 && (
                <span aria-label={`${unread} unread`} className="rounded-full bg-primary px-1.5 text-xs text-primary-foreground">
                  {unread > 99 ? '99+' : unread}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
      </header>

      <main className={cn('mx-auto px-4 py-6', home ? 'max-w-2xl' : 'max-w-5xl')}>
        <Outlet context={{ searchQuery: debouncedQuery }} />
      </main>
    </div>
  )
}
