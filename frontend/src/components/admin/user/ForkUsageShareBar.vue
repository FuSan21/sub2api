<template>
  <!-- [fork] Usage share window: assigned share (light) and used share (solid) of the whole pool -->
  <div class="flex items-center gap-1" :title="tooltip">
    <span :class="['w-[32px] shrink-0 rounded px-1 text-center text-[10px] font-medium', palette.badge]">
      {{ label }}
    </span>

    <div class="relative h-1.5 w-20 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
      <div
        v-if="assigned !== null"
        :class="['absolute inset-y-0 left-0', palette.assigned]"
        :style="{ width: pct(assigned) }"
      ></div>
      <div
        :class="['absolute inset-y-0 left-0 transition-all duration-300', over ? 'bg-red-500' : palette.used]"
        :style="{ width: pct(used) }"
      ></div>
    </div>

    <span :class="['shrink-0 text-[10px] font-medium', over ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-300']">
      {{ used.toFixed(1) }}<template v-if="assigned !== null">/{{ trim(assigned) }}</template>%
    </span>

    <span v-if="countdown" class="shrink-0 text-[10px] text-gray-400">{{ countdown }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useIntervalFn } from '@vueuse/core'

const props = defineProps<{
  label: string
  used: number // % of the pool
  limit: number | null // % of the pool; null or >= 100 = no limit
  resetsAt?: string | null
  color: 'indigo' | 'emerald'
}>()

const palettes = {
  indigo: {
    badge: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300',
    assigned: 'bg-indigo-200 dark:bg-indigo-900/60',
    used: 'bg-indigo-500'
  },
  emerald: {
    badge: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300',
    assigned: 'bg-emerald-200 dark:bg-emerald-900/60',
    used: 'bg-emerald-500'
  }
}
const palette = computed(() => palettes[props.color])

const assigned = computed(() => (props.limit !== null && props.limit < 100 ? props.limit : null))
const over = computed(() => assigned.value !== null && props.used >= assigned.value)

const pct = (v: number) => `${Math.min(Math.max(v, 0), 100)}%`
const trim = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(1))

const tooltip = computed(() => {
  const base = `${props.label}: ${props.used.toFixed(1)}% of the pool used`
  if (assigned.value === null) return `${base} · no limit`
  return `${base} · assigned ${trim(assigned.value)}% (${Math.max(0, assigned.value - props.used).toFixed(1)}% left)`
})

const now = ref(Date.now())
useIntervalFn(() => (now.value = Date.now()), 60_000)

const countdown = computed(() => {
  if (!props.resetsAt) return ''
  const mins = Math.round((new Date(props.resetsAt).getTime() - now.value) / 60_000)
  if (mins <= 0) return ''
  const d = Math.floor(mins / 1440)
  const h = Math.floor((mins % 1440) / 60)
  const m = mins % 60
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`
})
</script>
