<template>
  <!-- [fork] Usage share column on the admin Users page: each live subscription's usage vs. quota in x -->
  <div v-if="rows.length" class="min-w-[220px] space-y-1.5">
    <div v-for="s in rows" :key="s.subscription_id ?? s.group_id">
      <div v-if="rows.length > 1" class="mb-0.5 truncate text-[10px] text-gray-400" :title="s.group_name">
        {{ s.group_name }}
      </div>
      <div class="space-y-1">
        <ForkUsageShareBar label="5h" :used="s.used_5h_x" :limit="s.quota_5h_x" :capacity="s.capacity_5h_x" :reset-at="s.next_drop_5h" color="indigo" />
        <ForkUsageShareBar label="7d" :used="s.used_7d_x" :limit="s.quota_7d_x" :capacity="s.capacity_7d_x" :reset-at="s.next_drop_7d" color="emerald" />
      </div>
      <div v-if="s.blocked" class="mt-0.5 text-[10px] font-medium text-red-600 dark:text-red-400">
        blocked<template v-if="s.frees_at"> · frees {{ formatTime(s.frees_at) }}</template>
      </div>
      <div v-else-if="s.frees_at" class="mt-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
        over quota · spare capacity only
      </div>
    </div>
  </div>
  <span v-else class="text-xs text-gray-400">—</span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import ForkUsageShareBar from './ForkUsageShareBar.vue'
import { useForkUsageShareStatuses } from '@/composables/forkUsageShareStatuses'

const props = defineProps<{ userId: number }>()

const { statuses, ensureLoaded } = useForkUsageShareStatuses()
void ensureLoaded()
const rows = computed(() => statuses.value.filter(s => s.user_id === props.userId))

const formatTime = (iso: string) =>
  new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
</script>
