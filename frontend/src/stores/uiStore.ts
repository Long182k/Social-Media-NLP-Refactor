import { create } from 'zustand'
import type { PublicUser } from '@/types/api'

export type Theme = 'light' | 'dark'

const THEME_STORAGE_KEY = 'smn_theme'

function getInitialTheme(): Theme {
  if (typeof window === 'undefined') return 'light'
  try {
    const saved = window.localStorage.getItem(THEME_STORAGE_KEY)
    if (saved === 'dark' || saved === 'light') return saved
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

interface UIState {
  accessToken: string | null
  user: PublicUser | null
  authReady: boolean
  sessionRestoreError: boolean
  theme: Theme
  setSession: (accessToken: string, user: PublicUser) => void
  clearSession: () => void
  setAuthReady: (ready: boolean) => void
  setSessionRestoreError: (hasError: boolean) => void
  toggleTheme: () => void
}

export const useUIStore = create<UIState>((set) => ({
  accessToken: null,
  user: null,
  authReady: false,
  sessionRestoreError: false,
  theme: getInitialTheme(),
  setSession: (accessToken, user) => set({ accessToken, user, sessionRestoreError: false }),
  clearSession: () => set({ accessToken: null, user: null, sessionRestoreError: false }),
  setAuthReady: (authReady) => set({ authReady }),
  setSessionRestoreError: (sessionRestoreError) => set({ sessionRestoreError }),
  toggleTheme: () =>
    set((state) => {
      const next: Theme = state.theme === 'dark' ? 'light' : 'dark'
      try {
        window.localStorage.setItem(THEME_STORAGE_KEY, next)
      } catch {
        // Ignore storage errors in restricted contexts
      }
      return { theme: next }
    }),
}))
