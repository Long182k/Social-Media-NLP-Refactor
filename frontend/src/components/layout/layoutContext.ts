import { useOutletContext } from 'react-router-dom'

export interface AppLayoutContext {
  searchQuery: string
}

export function useAppLayoutContext() {
  return useOutletContext<AppLayoutContext>()
}
