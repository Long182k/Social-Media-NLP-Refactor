import { apiFetch } from '@/lib/api'
import type { AuthResponse, MessageResponse, PublicUser } from '@/types/api'

export interface LoginPayload {
  identifier: string
  password: string
}

export interface RegisterPayload {
  username: string
  nickName?: string
  email: string
  password: string
}

export interface ForgotPasswordPayload {
  email: string
}

export interface ResetPasswordPayload {
  token: string
  newPassword: string
}

export const authApi = {
  login(payload: LoginPayload): Promise<AuthResponse> {
    return apiFetch<AuthResponse>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  register(payload: RegisterPayload): Promise<AuthResponse> {
    return apiFetch<AuthResponse>('/api/auth/register', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  signout(): Promise<MessageResponse> {
    return apiFetch<MessageResponse>('/api/auth/signout', {
      method: 'POST',
    })
  },

  forgotPassword(payload: ForgotPasswordPayload): Promise<MessageResponse> {
    return apiFetch<MessageResponse>('/api/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  resetPassword(payload: ResetPasswordPayload): Promise<MessageResponse> {
    return apiFetch<MessageResponse>('/api/auth/reset-password', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  getMe(): Promise<PublicUser> {
    return apiFetch<PublicUser>('/api/auth/me')
  },
}
