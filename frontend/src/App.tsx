import { lazy, Suspense, useEffect } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AppLayout } from '@/components/layout/AppLayout'
import { ProtectedRoute, PublicOnlyRoute } from '@/components/routing/RouteGuards'
import { refreshAccessToken } from '@/lib/api'
import { useUIStore } from '@/stores/uiStore'

const HomePage = lazy(() => import('@/pages/HomePage'))
const LoginPage = lazy(() => import('@/pages/LoginPage'))
const RegisterPage = lazy(() => import('@/pages/RegisterPage'))
const ResetPasswordPage = lazy(() => import('@/pages/ResetPasswordPage'))
const BookmarksPage = lazy(() => import('@/pages/BookmarksPage'))
const GroupsPage = lazy(() => import('@/pages/GroupsPage'))
const GroupPage = lazy(() => import('@/pages/GroupPage'))
const EventsPage = lazy(() => import('@/pages/EventsPage'))
const EventPage = lazy(() => import('@/pages/EventPage'))
const ProfilePage = lazy(() => import('@/pages/ProfilePage'))
const SettingsPage = lazy(() => import('@/pages/SettingsPage'))
const MessagesPage = lazy(() => import('@/pages/MessagesPage'))
const NotificationsPage = lazy(() => import('@/pages/NotificationsPage'))
const AdminPage = lazy(() => import('@/pages/AdminPage'))
const PostPage = lazy(() => import('@/pages/PostPage'))

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

function RouteFallback() {
  return (
    <div
      className="flex min-h-[50dvh] items-center justify-center p-6"
      role="status"
      aria-live="polite"
    >
      <div className="h-7 w-7 animate-spin rounded-full border-2 border-primary border-t-transparent" />
    </div>
  )
}

let bootstrapStarted = false

function bootstrapAuth(setAuthReady: (ready: boolean) => void) {
  if (bootstrapStarted) return
  bootstrapStarted = true
  refreshAccessToken().finally(() => {
    setAuthReady(true)
  })
}

export default function App() {
  const theme = useUIStore((s) => s.theme)
  const setAuthReady = useUIStore((s) => s.setAuthReady)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
  }, [theme])

  useEffect(() => {
    bootstrapAuth(setAuthReady)
  }, [setAuthReady])

  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Suspense fallback={<RouteFallback />}>
          <Routes>
            <Route element={<PublicOnlyRoute />}>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/register" element={<RegisterPage />} />
            </Route>

            <Route path="/reset-password" element={<ResetPasswordPage />} />

            <Route element={<ProtectedRoute />}>
              <Route element={<AppLayout />}>
                <Route path="/" element={<HomePage />} />
                <Route path="/posts/:id" element={<PostPage />} />
                <Route path="/bookmarks" element={<BookmarksPage />} />
                <Route path="/groups" element={<GroupsPage />} />
                <Route path="/groups/:id" element={<GroupPage />} />
                <Route path="/events" element={<EventsPage />} />
                <Route path="/events/:id" element={<EventPage />} />
                <Route path="/profile/:id" element={<ProfilePage />} />
                <Route path="/settings" element={<SettingsPage />} />
                <Route path="/messages" element={<MessagesPage />} />
                <Route path="/messages/:id" element={<MessagesPage />} />
                <Route path="/notifications" element={<NotificationsPage />} />
                <Route path="/admin" element={<AdminPage />} />
              </Route>
            </Route>

            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
