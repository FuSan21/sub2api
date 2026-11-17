<template>
  <!-- [fork] Usage share: the user's usage of each subscription's quota in Pro-equivalents -->
  <div v-if="statuses.length" class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">Claude usage</h2>
      <p class="text-xs text-gray-500 dark:text-dark-400">
        Measured in Pro-equivalents: 1x = the full 5-hour or weekly limit of one Claude Pro ($20) plan.
      </p>
    </div>
    <div class="grid gap-4 p-4 md:grid-cols-2">
      <div v-for="s in statuses" :key="s.subscription_id ?? s.group_id" class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800/50">
        <div class="mb-3 flex items-center justify-between">
          <span class="text-sm font-medium text-gray-900 dark:text-white">{{ s.group_name }}</span>
          <span v-if="s.blocked" class="text-xs font-medium text-red-600 dark:text-red-400">quota reached</span>
          <span v-else-if="s.frees_at" class="text-xs font-medium text-amber-600 dark:text-amber-400">over quota · spare capacity only</span>
        </div>
        <div v-for="w in windows(s)" :key="w.label" class="mb-3 last:mb-0">
          <div class="flex justify-between text-xs text-gray-600 dark:text-gray-400">
            <span>{{ w.label }}</span>
            <span>
              {{ formatX(w.used) }} used
              <template v-if="w.quota !== null"> · {{ formatX(w.remaining) }} of {{ formatX(w.quota) }} left</template>
              <template v-else> · no quota</template>
            </span>
          </div>
          <div class="mt-1 h-2 w-full rounded bg-gray-200 dark:bg-dark-600">
            <div
              :class="['h-2 rounded', w.quota !== null && w.used >= w.quota ? 'bg-red-500' : 'bg-primary-500']"
              :style="{ width: `${barWidth(w.used, w.quota, w.capacity)}%` }"
            />
          </div>
          <div v-if="w.nextDrop && w.used > 0" class="mt-0.5 text-[11px] text-gray-400">
            part of it frees up {{ formatTime(w.nextDrop) }}
          </div>
        </div>
        <p v-if="s.frees_at" class="text-xs text-gray-500 dark:text-dark-400">Frees up around {{ formatTime(s.frees_at) }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { formatX, getMyUsageShare, type UsageShareStatus } from '@/api/admin/forkUsageShare'

const statuses = ref<UsageShareStatus[]>([])

const windows = (s: UsageShareStatus) => [
  { label: '5-hour', used: s.used_5h_x, quota: s.quota_5h_x, remaining: s.remaining_5h_x, capacity: s.capacity_5h_x, nextDrop: s.next_drop_5h },
  { label: 'Weekly', used: s.used_7d_x, quota: s.quota_7d_x, remaining: s.remaining_7d_x, capacity: s.capacity_7d_x, nextDrop: s.next_drop_7d }
]

const barWidth = (used: number, quota: number | null, capacity: number) => {
  const max = quota !== null ? quota : capacity
  if (!max || max <= 0) return used > 0 ? '100' : '0'
  return Math.min(100, (used / max) * 100).toFixed(1)
}

const formatTime = (iso: string) =>
  new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })

onMounted(async () => {
  try {
    statuses.value = (await getMyUsageShare()).filter(s => s.accounts > 0)
  } catch {
    statuses.value = [] // optional card; stay hidden on errors
  }
})
</script>
