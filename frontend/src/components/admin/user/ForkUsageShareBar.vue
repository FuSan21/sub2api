<template>
  <!-- [fork] Usage share window in x (1x = one Claude Pro account): used vs. quota -->
  <div class="flex items-center gap-1" :title="tooltip">
    <span v-if="label" :class="['w-[32px] shrink-0 rounded px-1 text-center text-[10px] font-medium', palette.badge]">
      {{ label }}
    </span>

    <div class="relative h-1.5 w-20 shrink-0 overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
      <div
        :class="['absolute inset-y-0 left-0 transition-all duration-300', over ? 'bg-red-500' : palette.used]"
        :style="{ width: width }"
      ></div>
    </div>

    <span :class="['shrink-0 text-[10px] font-medium', over ? 'text-red-600 dark:text-red-400' : 'text-gray-700 dark:text-gray-300']">
      {{ trim(used) }}<template v-if="limit !== null">/{{ trim(limit) }}</template>x
    </span>

    <span v-if="countdown" class="shrink-0 text-[10px] text-gray-400">{{ countdown }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useIntervalFn } from '@vueuse/core'

const props = withDefaults(
  defineProps<{
    label?: string
    used: number // x
    limit: number | null // quota in x; null = unlimited
    capacity?: number // pool size in x, scales the bar when unlimited
    resetAt?: string | null
    color?: 'indigo' | 'emerald'
  }>(),
  { label: '', capacity: 0, resetAt: null, color: 'indigo' }
)

const palettes = {
  indigo: { badge: 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300', used: 'bg-indigo-500' },
  emerald: { badge: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300', used: 'bg-emerald-500' }
}
const palette = computed(() => palettes[props.color])

const over = computed(() => props.limit !== null && props.used >= props.limit)
const width = computed(() => {
  const max = props.limit !== null ? props.limit : props.capacity
  if (!max || max <= 0) return props.used > 0 ? '100%' : '0%'
  return `${Math.min(100, Math.max(0, (props.used / max) * 100)).toFixed(1)}%`
})

const trim = (v: number) => String(Number(v.toFixed(v >= 10 ? 1 : 2)))

const tooltip = computed(() => {
  const base = `${props.label ? props.label + ': ' : ''}${trim(props.used)}x used (1x = one Claude Pro account)`
  if (props.limit === null) return `${base} · no quota`
  return `${base} · quota ${trim(props.limit)}x (${trim(Math.max(0, props.limit - props.used))}x left)`
})

const now = ref(Date.now())
useIntervalFn(() => (now.value = Date.now()), 60_000)

const countdown = computed(() => {
  if (!props.resetAt) return ''
  const mins = Math.round((new Date(props.resetAt).getTime() - now.value) / 60_000)
  if (mins <= 0) return ''
  const d = Math.floor(mins / 1440)
  const h = Math.floor((mins % 1440) / 60)
  const m = mins % 60
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`
})
</script>
