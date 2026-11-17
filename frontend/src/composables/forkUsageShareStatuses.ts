// [fork] Usage share: one shared request for every live subscription's status,
// used by the admin Users and Subscriptions table cells (refreshed at most every 30s).
import { ref } from 'vue'
import { getAllUsageShareStatuses, type UsageShareStatus } from '@/api/admin/forkUsageShare'

const statuses = ref<UsageShareStatus[]>([])
let loadedAt = 0
let inflight: Promise<void> | null = null

export function useForkUsageShareStatuses() {
  const ensureLoaded = (force = false) => {
    if (inflight || (!force && Date.now() - loadedAt < 30_000)) return inflight ?? Promise.resolve()
    inflight = getAllUsageShareStatuses()
      .then(list => {
        statuses.value = list
      })
      .catch(() => {})
      .finally(() => {
        loadedAt = Date.now()
        inflight = null
      })
    return inflight
  }
  return { statuses, ensureLoaded }
}
