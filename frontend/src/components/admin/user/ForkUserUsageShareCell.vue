<template>
  <!-- [fork] Usage share column on the admin Users page, styled like the Accounts usage windows -->
  <div v-if="rows.length" class="min-w-[220px] space-y-1.5">
    <div v-for="s in rows" :key="s.group_id">
      <div v-if="rows.length > 1" class="mb-0.5 truncate text-[10px] text-gray-400" :title="s.group_name">
        {{ s.group_name }}
      </div>
      <div class="space-y-1">
        <ForkUsageShareBar label="5h" :used="s.used_5h_pct" :limit="s.limit_5h_pct" :resets-at="s.next_drop_5h" color="indigo" />
        <ForkUsageShareBar label="7d" :used="s.used_7d_pct" :limit="s.limit_7d_pct" :resets-at="s.next_drop_7d" color="emerald" />
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
import ForkUsageShareBar from './ForkUsageShareBar.vue'

const props = defineProps<{ userId: number }>()

ensureLoaded()
const rows = computed(() => byUser.value[props.userId] ?? [])

const formatTime = (iso: string) =>
  new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
</script>
