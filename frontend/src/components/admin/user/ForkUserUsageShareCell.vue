<template>
  <!-- [fork] Usage share column on the admin Users page, styled like the Accounts usage windows -->
  <div v-if="rows.length" class="min-w-[180px] space-y-1.5">
    <div v-for="s in rows" :key="s.group_id">
      <div v-if="rows.length > 1" class="mb-0.5 truncate text-[10px] text-gray-400" :title="s.group_name">
        {{ s.group_name }}
      </div>
      <div class="space-y-1">
        <div :title="tooltip(s, '5h')">
          <UsageProgressBar
            label="5h"
            :utilization="barPct(s.used_5h_pct, s.limit_5h_pct)"
            :resets-at="s.next_drop_5h ?? null"
            color="indigo"
          />
        </div>
        <div :title="tooltip(s, '7d')">
          <UsageProgressBar
            label="7d"
            :utilization="barPct(s.used_7d_pct, s.limit_7d_pct)"
            :resets-at="s.next_drop_7d ?? null"
            color="emerald"
          />
        </div>
      </div>
      <div v-if="s.blocked" class="mt-0.5 text-[10px] font-medium text-red-600 dark:text-red-400">
        blocked<template v-if="s.frees_at"> · frees {{ formatTime(s.frees_at) }}</template>
      </div>
      <div v-else-if="s.frees_at" class="mt-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
        over limit · spare capacity only
      </div>
    </div>
  </div>
  <span v-else class="text-xs text-gray-400">—</span>
</template>

<script lang="ts">
import { ref } from 'vue'
import { getAllUsersUsageShare, type UsageShareStatus } from '@/api/admin/forkUsageShare'

// One request for the whole table, refreshed at most every 30s.
const byUser = ref<Record<number, UsageShareStatus[]>>({})
let loadedAt = 0
let inflight: Promise<void> | null = null

function ensureLoaded() {
  if (inflight || Date.now() - loadedAt < 30_000) return
  inflight = getAllUsersUsageShare()
    .then(list => {
      const map: Record<number, UsageShareStatus[]> = {}
      for (const s of list) {
        if (s.accounts > 0) (map[s.user_id] ??= []).push(s)
      }
      byUser.value = map
      loadedAt = Date.now()
    })
    .catch(() => {
      loadedAt = Date.now()
    })
    .finally(() => {
      inflight = null
    })
}
</script>

<script setup lang="ts">
import { computed } from 'vue'
import UsageProgressBar from '@/components/account/UsageProgressBar.vue'

const props = defineProps<{ userId: number }>()

ensureLoaded()
const rows = computed(() => byUser.value[props.userId] ?? [])

// With a limit the bar shows how much of the user's own allowance is used;
// without one, how much of the whole pool.
const barPct = (used: number, limit: number | null) =>
  Math.round((limit !== null && limit > 0 && limit < 100 ? (used / limit) * 100 : used) * 10) / 10

const tooltip = (s: UsageShareStatus, w: '5h' | '7d') => {
  const used = w === '5h' ? s.used_5h_pct : s.used_7d_pct
  const limit = w === '5h' ? s.limit_5h_pct : s.limit_7d_pct
  const base = `${w}: ${used.toFixed(1)}% of the pool used`
  if (limit === null || limit >= 100) return `${base} · no limit`
  return `${base} · limit ${limit}% (${Math.max(0, limit - used).toFixed(1)}% left)`
}

const formatTime = (iso: string) =>
  new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
</script>
