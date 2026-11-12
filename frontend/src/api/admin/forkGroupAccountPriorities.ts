// [fork] Per-group account priority overrides.
import { apiClient } from '../client'

export interface GroupAccountPriority {
  account_id: number
  name: string
  platform: string
  type: string
  status: string
  schedulable: boolean
  account_priority: number
  override_priority: number | null
  effective_priority: number
}

export interface GroupAccountPriorityUpdate {
  account_id: number
  priority: number | null
}

export async function getGroupAccountPriorities(groupId: number): Promise<GroupAccountPriority[]> {
  const { data } = await apiClient.get<GroupAccountPriority[]>(
    `/admin/groups/${groupId}/account-priorities`
  )
  return data ?? []
}

export async function setGroupAccountPriorities(
  groupId: number,
  items: GroupAccountPriorityUpdate[]
): Promise<GroupAccountPriority[]> {
  const { data } = await apiClient.put<GroupAccountPriority[]>(
    `/admin/groups/${groupId}/account-priorities`,
    { items }
  )
  return data ?? []
}
