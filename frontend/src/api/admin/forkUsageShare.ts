// [fork] Usage share: per-user consumption of the group's account pool.
import { apiClient } from '../client'

export interface UsageShareUserLimit {
  user_id: number
  limit_7d_pct: number | null
  limit_5h_pct: number | null
}

export interface UsageShareSettings {
  group_id: number
  enabled: boolean
  default_7d_pct: number | null
  default_5h_pct: number | null
  spare_7d_min_pct: number
  spare_7d_within_hours: number
  spare_5h_min_pct: number
  spare_5h_within_minutes: number
  limits: UsageShareUserLimit[]
}

export interface UsageShareAccount {
  id: number
  name: string
  util_5h_pct: number | null
  reset_5h: string | null
  util_7d_pct: number | null
  reset_7d: string | null
  outside_5h_pct: number
  outside_7d_pct: number
  by_user_5h_pct: Record<string, number>
  by_user_7d_pct: Record<string, number>
}

export interface UsageShareUser {
  id: number
  email: string
  username: string
  used_5h_pct: number
  used_7d_pct: number
  limit_5h_pct: number | null
  limit_7d_pct: number | null
  override: UsageShareUserLimit | null
  blocked: boolean
  frees_at?: string
}

export interface UsageShareReport {
  settings: UsageShareSettings
  accounts: UsageShareAccount[]
  users: UsageShareUser[]
}

export async function getUsageShare(groupId: number): Promise<UsageShareReport> {
  const { data } = await apiClient.get<UsageShareReport>(`/admin/groups/${groupId}/usage-share`)
  return data
}

export async function setUsageShare(groupId: number, settings: UsageShareSettings): Promise<UsageShareReport> {
  const { data } = await apiClient.put<UsageShareReport>(`/admin/groups/${groupId}/usage-share`, settings)
  return data
}
