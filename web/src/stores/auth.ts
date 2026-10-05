import { create } from 'zustand'
import type { UserInfo } from '@/types/permission'

const STORAGE_KEY = 'sf.auth'

/** 持久化会话结构（与 /api/auth/me 组装结果同构，登录即存、me 到位后补全权限快照） */
export interface StoredSession {
  /** Access Token（client.ts 请求拦截器与路由守卫共用字段名） */
  token: string
  refreshToken: string | null
  user: UserInfo | null
  /** 三段冻结权限码快照（MeResult.permissions） */
  permissions: string[]
  isSuper: boolean
  /** 首登强制改密门禁标志（middleware.go:74-78） */
  mustChangePassword: boolean
}

/** setSession 入参：token 必带，其余字段缺省时按零值落库 */
export type SessionInput = Partial<Omit<StoredSession, 'token'>> & Pick<StoredSession, 'token'>

function loadSession(): StoredSession | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<StoredSession> | null
    if (!parsed || typeof parsed.token !== 'string' || parsed.token === '') return null
    return {
      token: parsed.token,
      refreshToken: typeof parsed.refreshToken === 'string' ? parsed.refreshToken : null,
      user: parsed.user ?? null,
      permissions: Array.isArray(parsed.permissions) ? parsed.permissions : [],
      isSuper: parsed.isSuper === true,
      mustChangePassword: parsed.mustChangePassword === true,
    }
  } catch {
    return null
  }
}

function persist(session: StoredSession): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(session))
}

interface AuthState {
  /** 未登录为 null；持久化结构 StoredSession.token 恒为字符串 */
  token: string | null
  refreshToken: string | null
  user: UserInfo | null
  /** 三段冻结权限码快照（MeResult.permissions） */
  permissions: string[]
  isSuper: boolean
  /** 首登强制改密门禁标志（middleware.go:74-78） */
  mustChangePassword: boolean
  setSession: (session: SessionInput) => void
  /** 强制改密标志（api/client.ts 对 403 AUTH_PASSWORD_CHANGE_REQUIRED 特判置位） */
  setMustChangePassword: (flag: boolean) => void
  clearSession: () => void
}

/** 登录态分片（frontend.md §18.1：zustand 按域分片，不建巨型 Store） */
export const useAuthStore = create<AuthState>((set) => {
  const initial = loadSession()
  return {
    token: initial?.token ?? null,
    refreshToken: initial?.refreshToken ?? null,
    user: initial?.user ?? null,
    permissions: initial?.permissions ?? [],
    isSuper: initial?.isSuper ?? false,
    mustChangePassword: initial?.mustChangePassword ?? false,
    setSession: (session) => {
      // is_super 位于 MeResult 顶层而非 MeResult.user（service_auth.go:444 vs :474），
      // 而菜单/按钮的 canAccess 判据是 user.is_super——此处统一并入，登录/自愈/刷新三路径全覆盖。
      // permissions 同理：MeResult.permissions 在顶层，而 canAccess 读 user.permissions
      // （types/permission.ts:94）——不并入则非超管用户恒为空集，带码子菜单全部被
      // fail-closed 滤空（侧边栏"分组能展开但没有子项"）。登录响应不含权限/超管标记，
      // 由 PcLayout 会话自愈拉 /api/auth/me 补全（auth.ts StoredSession 注）。
      const nextUser = session.user
        ? {
            ...session.user,
            is_super: session.isSuper ?? session.user.is_super,
            permissions: session.permissions ?? session.user.permissions,
          }
        : null
      const next: StoredSession = {
        token: session.token,
        refreshToken: session.refreshToken ?? null,
        user: nextUser,
        permissions: session.permissions ?? [],
        isSuper: session.isSuper ?? false,
        mustChangePassword: session.mustChangePassword ?? false,
      }
      persist(next)
      set(next)
    },
    setMustChangePassword: (flag) => {
      set((state) => {
        persist({
          token: state.token ?? '',
          refreshToken: state.refreshToken,
          user: state.user,
          permissions: state.permissions,
          isSuper: state.isSuper,
          mustChangePassword: flag,
        })
        return { mustChangePassword: flag }
      })
    },
    clearSession: () => {
      localStorage.removeItem(STORAGE_KEY)
      set({
        token: null,
        refreshToken: null,
        user: null,
        permissions: [],
        isSuper: false,
        mustChangePassword: false,
      })
    },
  }
})
