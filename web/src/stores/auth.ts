import { create } from 'zustand'
import type { UserInfo } from '@/types/permission'

const STORAGE_KEY = 'sf.auth'

interface StoredSession {
  token: string
  user: UserInfo
}

function loadSession(): StoredSession | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as StoredSession) : null
  } catch {
    return null
  }
}

interface AuthState {
  token: string | null
  user: UserInfo | null
  setSession: (session: StoredSession) => void
  clearSession: () => void
}

/** 登录态分片（frontend.md §18.1：zustand 按域分片，不建巨型 Store） */
export const useAuthStore = create<AuthState>((set) => ({
  token: loadSession()?.token ?? null,
  user: loadSession()?.user ?? null,
  setSession: ({ token, user }) => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ token, user }))
    set({ token, user })
  },
  clearSession: () => {
    localStorage.removeItem(STORAGE_KEY)
    set({ token: null, user: null })
  },
}))
