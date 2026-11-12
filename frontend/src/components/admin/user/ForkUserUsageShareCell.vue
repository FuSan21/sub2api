<template>
  <!-- [fork] Usage share column on the admin Users page -->
  <div v-if="rows.length" class="space-y-0.5 text-xs">
    <div v-for="s in rows" :key="s.group_id" :title="s.group_name" class="whitespace-nowrap">
      <span class="text-gray-400">{{ s.group_name }}:</span>
      <span :class="cls(s.used_7d_pct, s.limit_7d_pct)"> 7d {{ fmt(s.used_7d_pct, s.limit_7d_pct) }}</span>
      <span :class="cls(s.used_5h_pct, s.limit_5h_pct)"> · 5h {{ fmt(s.used_5h_pct, s.limit_5h_pct) }}</span>
      <span v-if="s.blocked" class="text-red-600 dark:text-red-400"> · blocked</span>
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

const props = defineProps<{ userId: number }>()

ensureLoaded()
const rows = computed(() => byUser.value[props.userId] ?? [])

const fmt = (used: number, limit: number | null) => `${used.toFixed(1)}%` + (limit !== null ? `/${limit}%` : '')
const cls = (used: number, limit: number | null) =>
  limit !== null && used >= limit ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-300'
</script>
