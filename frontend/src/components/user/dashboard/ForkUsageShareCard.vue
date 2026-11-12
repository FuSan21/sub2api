<template>
  <!-- [fork] Usage share: the user's consumption of their group's account pool -->
  <div v-if="statuses.length" class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">Your share of the account pool</h2>
      <p class="text-xs text-gray-500 dark:text-dark-400">
        How much of the shared Claude accounts' 5-hour and weekly limits you've used, as % of your group's pool.
      </p>
    </div>
    <div class="grid gap-4 p-4 md:grid-cols-2">
      <div v-for="s in statuses" :key="s.group_id" class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800/50">
        <div class="mb-3 flex items-center justify-between">
          <span class="text-sm font-medium text-gray-900 dark:text-white">{{ s.group_name }}</span>
          <span v-if="s.blocked" class="text-xs font-medium text-red-600 dark:text-red-400">limit reached</span>
          <span v-else-if="s.frees_at" class="text-xs font-medium text-amber-600 dark:text-amber-400">over limit · spare capacity only</span>
        </div>
        <div v-for="w in windows(s)" :key="w.label" class="mb-3 last:mb-0">
          <div class="flex justify-between text-xs text-gray-600 dark:text-gray-400">
            <span>{{ w.label }}</span>
            <span>
              {{ w.used.toFixed(1) }}% used
              <template v-if="w.limit !== null"> · {{ w.remaining?.toFixed(1) }}% of {{ w.limit }}% left</template>
              <template v-else> · no limit</template>
            </span>
          </div>
          <div class="mt-1 h-2 w-full rounded bg-gray-200 dark:bg-dark-600">
            <div
              :class="['h-2 rounded', w.limit !== null && w.used >= w.limit ? 'bg-red-500' : 'bg-primary-500']"
              :style="{ width: `${barWidth(w.used, w.limit)}%` }"
            />
          </div>
        </div>
        <p v-if="s.frees_at" class="text-xs text-gray-500 dark:text-dark-400">
          Frees up around {{ new Date(s.frees_at).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' }) }}
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getMyUsageShare, type UsageShareStatus } from '@/api/admin/forkUsageShare'

const statuses = ref<UsageShareStatus[]>([])

const windows = (s: UsageShareStatus) => [
  { label: 'Weekly', used: s.used_7d_pct, limit: s.limit_7d_pct, remaining: s.remaining_7d_pct },
  { label: '5-hour', used: s.used_5h_pct, limit: s.limit_5h_pct, remaining: s.remaining_5h_pct }
]

const barWidth = (used: number, limit: number | null) =>
  Math.min(100, (used / (limit && limit > 0 ? limit : 100)) * 100).toFixed(1)

onMounted(async () => {
  try {
    statuses.value = (await getMyUsageShare()).filter(s => s.accounts > 0)
  } catch {
    statuses.value = [] // optional card; stay hidden on errors
  }
})
</script>
