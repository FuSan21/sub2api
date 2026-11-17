// [fork] Usage share: per-subscription quotas in Pro-account equivalents
// (1x = one full 5h / 7d window of a $20 Claude Pro account).
import { apiClient } from '../client'

export interface UsageQuota {
  quota_5h_x: number | null
  quota_7d_x: number | null
}

export interface UsageQuotaSettings {
  spare_7d_min_pct: number
  spare_7d_within_hours: number
  spare_5h_min_pct: number
  spare_5h_within_minutes: number
}

export type UsageQuotaSource = 'plan' | 'admin' | 'group' | 'none'

export interface UsageShareStatus {
  user_id: number
  email: string
  username: string
  group_id: number
  group_name: string
  subscription_id: number | null
  expires_at?: string
  quota_source: UsageQuotaSource
  enforced: boolean
  accounts: number
  capacity_5h_x: number
  capacity_7d_x: number
  used_5h_x: number
  used_7d_x: number
  quota_5h_x: number | null
  quota_7d_x: number | null
  remaining_5h_x: number | null
  remaining_7d_x: number | null
  blocked: boolean
  frees_at?: string
  next_drop_5h?: string
  next_drop_7d?: string
}

export interface UsageShareAccount {
  id: number
  name: string
  type: string
  tier: string | null
  detected_at: string | null
  detect_error: string | null
  weight_5h: number
  weight_7d: number
  weight_5h_override: number | null
  weight_7d_override: number | null
  /** Ultra account without a 5-hour window: excluded from 5h capacity, always spare for 5h. */
  no_5h_limit: boolean
  util_5h_pct: number | null
  reset_5h: string | null
  util_7d_pct: number | null
  reset_7d: string | null
  by_subscription_5h_x: Record<string, number>
  by_subscription_7d_x: Record<string, number>
  other_5h_x: number
  other_7d_x: number
  outside_5h_x: number
  outside_7d_x: number
}

export interface UsageShareReport {
  group_id: number
  default: UsageQuota | null
  settings: UsageQuotaSettings
  capacity_5h_x: number
  capacity_7d_x: number
  allocated_5h_x: number
  allocated_7d_x: number
  unlimited_5h: number
  unlimited_7d: number
  accounts_without_5h_limit: number
  accounts: UsageShareAccount[]
  subscriptions: UsageShareStatus[]
}

export async function getUsageShare(groupId: number): Promise<UsageShareReport> {
  const { data } = await apiClient.get<UsageShareReport>(`/admin/groups/${groupId}/usage-share`)
  return data
}

/** Sets (or with null clears) the group's default subscription quota. */
export async function setGroupDefaultQuota(groupId: number, quota: UsageQuota | null): Promise<UsageShareReport> {
  const { data } = await apiClient.put<UsageShareReport>(`/admin/groups/${groupId}/usage-share`, { default: quota })
  return data
}

export async function getUsageQuotaSettings(): Promise<UsageQuotaSettings> {
  const { data } = await apiClient.get<UsageQuotaSettings>('/admin/usage-share/settings')
  return data
}

export async function setUsageQuotaSettings(settings: UsageQuotaSettings): Promise<UsageQuotaSettings> {
  const { data } = await apiClient.put<UsageQuotaSettings>('/admin/usage-share/settings', settings)
  return data
}

export async function setAccountWeights(
  accountId: number,
  input: { weight_5h_override: number | null; weight_7d_override: number | null; redetect?: boolean }
): Promise<void> {
  await apiClient.put(`/admin/usage-share/accounts/${accountId}`, input)
}

/** Sets a subscription's own quota, or with null falls back to the group default. */
export async function setSubscriptionQuota(subscriptionId: number, quota: UsageQuota | null): Promise<void> {
  await apiClient.put(`/admin/usage-share/subscriptions/${subscriptionId}`, quota ?? { clear: true })
}

export async function getPlanQuotas(): Promise<Record<string, UsageQuota>> {
  const { data } = await apiClient.get<Record<string, UsageQuota>>('/admin/usage-share/plans')
  return data ?? {}
}

/** Sets what buying the plan grants, or with null removes it. */
export async function setPlanQuota(planId: number, quota: UsageQuota | null): Promise<void> {
  await apiClient.put(`/admin/usage-share/plans/${planId}`, quota ?? { clear: true })
}

export async function getMyUsageShare(): Promise<UsageShareStatus[]> {
  const { data } = await apiClient.get<UsageShareStatus[]>('/user/usage-share')
  return data ?? []
}

export async function getAllUsageShareStatuses(): Promise<UsageShareStatus[]> {
  const { data } = await apiClient.get<UsageShareStatus[]>('/admin/usage-share/subscriptions')
  return data ?? []
}

// --- shared formatting -------------------------------------------------------

export const formatX = (v: number | null | undefined, digits = 2): string =>
  v === null || v === undefined ? '—' : `${Number(v.toFixed(digits))}x`

export const tierLabel = (tier: string | null | undefined): string => {
  switch (tier) {
    case 'pro':
      return 'Pro'
    case 'max_5x':
      return 'Max (5x)'
    case 'max_20x':
      return 'Ultra (10x)'
    case null:
    case undefined:
      return 'not detected'
    default:
      return tier
  }
}

export const quotaSourceLabel = (s: UsageQuotaSource): string =>
  ({ plan: 'from plan', admin: 'set by admin', group: 'group default', none: 'no quota' })[s] ?? s

/** Parses a quota input: '' / null = unlimited (null), invalid = undefined. */
export const parseQuotaInput = (v: number | string | null | undefined, max = 10000): number | null | undefined => {
  if (v === null || v === undefined || v === '') return null
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 && n <= max ? n : undefined
}
