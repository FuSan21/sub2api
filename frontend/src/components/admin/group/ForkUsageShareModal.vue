<template>
  <!-- [fork] Usage share: per-user consumption of the group's account pool -->
  <BaseDialog :show="show" title="Usage share" width="extra-wide" @close="emit('close')">
    <div v-if="group" class="space-y-5">
      <div class="rounded-lg bg-gray-50 px-4 py-2.5 text-sm text-gray-600 dark:bg-dark-700 dark:text-gray-400">
        <span class="font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
        — each rise of an account's 5h / 7d utilization is split across the users active on it (by cost).
        Usage is shown as % of the group's pool ({{ accountCount }} account{{ accountCount === 1 ? '' : 's' }};
        100% = all of them fully used) and follows each account's own reset.
      </div>

      <div v-if="loading" class="flex justify-center py-6">
        <Icon name="refresh" size="md" class="animate-spin text-primary-500" />
      </div>

      <template v-else-if="report">
        <!-- settings -->
        <div class="grid gap-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600 md:grid-cols-2">
          <label class="flex items-center gap-3 md:col-span-2">
            <Toggle v-model="form.enabled" />
            <span class="text-sm font-medium text-gray-900 dark:text-white">Enforce limits</span>
            <span class="text-xs text-gray-500">(usage is tracked either way)</span>
          </label>
          <div>
            <div class="mb-1 text-xs font-medium text-gray-500">Default limit per user (% of pool)</div>
            <div class="flex items-center gap-2 text-sm">
              <span class="w-10 text-gray-500">7d</span>
              <input v-model="form.default7d" type="number" min="0" max="100" step="0.5" placeholder="no limit" class="hide-spinner input w-28" />
              <span class="w-10 text-gray-500">5h</span>
              <input v-model="form.default5h" type="number" min="0" max="100" step="0.5" placeholder="no limit" class="hide-spinner input w-28" />
            </div>
          </div>
          <div>
            <div class="mb-1 text-xs font-medium text-gray-500">
              Over the limit, still allow an account that has at least … left and resets within …
            </div>
            <div class="flex flex-wrap items-center gap-2 text-sm">
              <span class="w-10 text-gray-500">7d</span>
              <input v-model="form.spare7dMin" type="number" min="0" max="100" class="hide-spinner input w-20" />%
              <input v-model="form.spare7dHours" type="number" min="0" class="hide-spinner input w-20" />h
              <span class="w-10 text-gray-500">5h</span>
              <input v-model="form.spare5hMin" type="number" min="0" max="100" class="hide-spinner input w-20" />%
              <input v-model="form.spare5hMinutes" type="number" min="0" class="hide-spinner input w-20" />min
            </div>
          </div>
        </div>

        <!-- users -->
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400">
                <th class="px-3 py-2">User</th>
                <th class="px-3 py-2">7d used / limit</th>
                <th class="px-3 py-2">5h used / limit</th>
                <th class="px-3 py-2">Override 7d %</th>
                <th class="px-3 py-2">Override 5h %</th>
                <th class="px-3 py-2">Status</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr v-for="u in report.users" :key="u.id">
                <td class="px-3 py-2">
                  <div class="text-gray-900 dark:text-white">{{ u.username || u.email }}</div>
                  <div v-if="u.username" class="text-xs text-gray-400">{{ u.email }}</div>
                </td>
                <td class="px-3 py-2">
                  <UsageBar :used="u.used_7d_pct" :limit="effectiveLimit(u.id, '7d')" />
                </td>
                <td class="px-3 py-2">
                  <UsageBar :used="u.used_5h_pct" :limit="effectiveLimit(u.id, '5h')" />
                </td>
                <td class="px-3 py-2">
                  <input v-model="overrides[u.id].l7" type="number" min="0" max="100" step="0.5" placeholder="default" class="hide-spinner input w-24" />
                </td>
                <td class="px-3 py-2">
                  <input v-model="overrides[u.id].l5" type="number" min="0" max="100" step="0.5" placeholder="default" class="hide-spinner input w-24" />
                </td>
                <td class="whitespace-nowrap px-3 py-2 text-xs">
                  <span v-if="u.blocked" class="text-red-600 dark:text-red-400">blocked</span>
                  <span v-else-if="u.frees_at" class="text-amber-600 dark:text-amber-400">over · spare only</span>
                  <span v-else class="text-green-600 dark:text-green-400">ok</span>
                  <div v-if="u.frees_at" class="text-gray-400">frees {{ formatTime(u.frees_at) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- accounts -->
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400">
                <th class="px-3 py-2">Account</th>
                <th class="px-3 py-2">7d window</th>
                <th class="px-3 py-2">5h window</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr v-for="a in report.accounts" :key="a.id">
                <td class="px-3 py-2 text-gray-900 dark:text-white">{{ a.name }}</td>
                <td class="px-3 py-2">
                  <AccountWindow :util="a.util_7d_pct" :reset="a.reset_7d" :by-user="a.by_user_7d_pct" :outside="a.outside_7d_pct" />
                </td>
                <td class="px-3 py-2">
                  <AccountWindow :util="a.util_5h_pct" :reset="a.reset_5h" :by-user="a.by_user_5h_pct" :outside="a.outside_5h_pct" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>

      <div class="flex items-center justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <span v-if="invalid" class="mr-auto text-xs text-red-600 dark:text-red-400">
          Percentages must be 0–100 and durations ≥ 0.
        </span>
        <button type="button" class="btn btn-sm px-4 py-1.5" :disabled="loading" @click="load">Refresh</button>
        <button type="button" class="btn btn-sm px-4 py-1.5" @click="emit('close')">Close</button>
        <button type="button" class="btn btn-primary btn-sm px-4 py-1.5" :disabled="saving || loading || invalid || !report" @click="save">
          <Icon v-if="saving" name="refresh" size="sm" class="mr-1 animate-spin" />
          Save
        </button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, reactive, ref, watch, type PropType } from 'vue'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'
import { getUsageShare, setUsageShare, type UsageShareReport } from '@/api/admin/forkUsageShare'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
}>()

type NumInput = number | string | null | undefined

const appStore = useAppStore()
const loading = ref(false)
const saving = ref(false)
const report = ref<UsageShareReport | null>(null)
const form = reactive({
  enabled: false,
  default7d: null as NumInput,
  default5h: null as NumInput,
  spare7dMin: 40 as NumInput,
  spare7dHours: 24 as NumInput,
  spare5hMin: 40 as NumInput,
  spare5hMinutes: 60 as NumInput
})
const overrides = ref<Record<number, { l7: NumInput; l5: NumInput }>>({})

// Returns null for empty, undefined for invalid.
const parseNum = (v: NumInput, max = Infinity): number | null | undefined => {
  if (v === null || v === undefined || v === '') return null
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 && n <= max ? n : undefined
}

const accountCount = computed(() => report.value?.accounts.length ?? 0)

const invalid = computed(() => {
  const checks = [
    parseNum(form.default7d, 100),
    parseNum(form.default5h, 100),
    parseNum(form.spare7dMin, 100),
    parseNum(form.spare5hMin, 100),
    parseNum(form.spare7dHours),
    parseNum(form.spare5hMinutes)
  ]
  for (const o of Object.values(overrides.value)) checks.push(parseNum(o.l7, 100), parseNum(o.l5, 100))
  return checks.some(v => v === undefined)
})

const effectiveLimit = (userId: number, w: '7d' | '5h'): number | null => {
  const o = overrides.value[userId]
  const own = parseNum(w === '7d' ? o?.l7 : o?.l5, 100)
  if (own !== null && own !== undefined) return own
  return parseNum(w === '7d' ? form.default7d : form.default5h, 100) ?? null
}

const formatTime = (iso: string | null | undefined) =>
  iso ? new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' }) : '—'

const applyReport = (r: UsageShareReport) => {
  report.value = r
  const s = r.settings
  form.enabled = s.enabled
  form.default7d = s.default_7d_pct
  form.default5h = s.default_5h_pct
  form.spare7dMin = s.spare_7d_min_pct
  form.spare7dHours = s.spare_7d_within_hours
  form.spare5hMin = s.spare_5h_min_pct
  form.spare5hMinutes = s.spare_5h_within_minutes
  overrides.value = Object.fromEntries(
    r.users.map(u => [u.id, { l7: u.override?.limit_7d_pct ?? null, l5: u.override?.limit_5h_pct ?? null }])
  )
}

const load = async () => {
  if (!props.group) return
  loading.value = true
  try {
    applyReport(await getUsageShare(props.group.id))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Failed to load usage share'))
  } finally {
    loading.value = false
  }
}

const save = async () => {
  if (!props.group || invalid.value) return
  saving.value = true
  try {
    const limits = Object.entries(overrides.value)
      .map(([id, o]) => ({ user_id: Number(id), limit_7d_pct: parseNum(o.l7, 100) ?? null, limit_5h_pct: parseNum(o.l5, 100) ?? null }))
      .filter(l => l.limit_7d_pct !== null || l.limit_5h_pct !== null)
    applyReport(
      await setUsageShare(props.group.id, {
        group_id: props.group.id,
        enabled: form.enabled,
        default_7d_pct: parseNum(form.default7d, 100) ?? null,
        default_5h_pct: parseNum(form.default5h, 100) ?? null,
        spare_7d_min_pct: parseNum(form.spare7dMin, 100) ?? 0,
        spare_7d_within_hours: Math.round(parseNum(form.spare7dHours) ?? 0),
        spare_5h_min_pct: parseNum(form.spare5hMin, 100) ?? 0,
        spare_5h_within_minutes: Math.round(parseNum(form.spare5hMinutes) ?? 0),
        limits
      })
    )
    appStore.showSuccess('Usage share saved')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Failed to save usage share'))
  } finally {
    saving.value = false
  }
}

const userName = (id: string) => {
  const u = report.value?.users.find(x => String(x.id) === id)
  return u ? u.username || u.email : `#${id}`
}

const UsageBar = defineComponent({
  props: { used: { type: Number, required: true }, limit: { type: Number as PropType<number | null>, default: null } },
  setup(p) {
    return () => {
      const max = p.limit && p.limit > 0 ? p.limit : 100
      const ratio = Math.min(1, p.used / max)
      const over = p.limit !== null && p.used >= p.limit
      return h('div', { class: 'min-w-[140px]' }, [
        h('div', { class: 'text-xs text-gray-700 dark:text-gray-300' },
          `${p.used.toFixed(1)}%` + (p.limit !== null ? ` / ${p.limit}%` : ' / no limit')),
        h('div', { class: 'mt-1 h-1.5 w-full rounded bg-gray-100 dark:bg-dark-600' }, [
          h('div', {
            class: ['h-1.5 rounded', over ? 'bg-red-500' : ratio > 0.8 ? 'bg-amber-500' : 'bg-primary-500'],
            style: { width: `${(ratio * 100).toFixed(1)}%` }
          })
        ])
      ])
    }
  }
})

const AccountWindow = defineComponent({
  props: {
    util: { type: Number as PropType<number | null>, default: null },
    reset: { type: String as PropType<string | null>, default: null },
    byUser: { type: Object as PropType<Record<string, number>>, required: true },
    outside: { type: Number, default: 0 }
  },
  setup(p) {
    return () => {
      if (p.util === null) return h('span', { class: 'text-xs text-gray-400' }, 'no data')
      const parts = Object.entries(p.byUser)
        .sort((a, b) => b[1] - a[1])
        .map(([id, pct]) => `${userName(id)} ${pct.toFixed(1)}%`)
      if (p.outside > 0.05) parts.push(`outside ${p.outside.toFixed(1)}%`)
      return h('div', { class: 'text-xs' }, [
        h('div', { class: 'text-gray-900 dark:text-white' }, `${p.util.toFixed(0)}% used · resets ${formatTime(p.reset)}`),
        h('div', { class: 'text-gray-500 dark:text-gray-400' }, parts.join(' · ') || '—')
      ])
    }
  }
})

watch(
  () => [props.show, props.group?.id] as const,
  ([show]) => {
    if (show) void load()
  },
  { immediate: true }
)
</script>
