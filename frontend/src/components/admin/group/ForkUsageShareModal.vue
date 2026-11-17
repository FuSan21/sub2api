<template>
  <!-- [fork] Usage share: subscription quotas in Pro-account equivalents -->
  <BaseDialog :show="show" title="Usage quotas" width="extra-wide" @close="emit('close')">
    <div v-if="group" class="space-y-5">
      <div class="rounded-lg bg-gray-50 px-4 py-2.5 text-sm text-gray-600 dark:bg-dark-700 dark:text-gray-400">
        <span class="font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
        — usage is measured in <b>x</b>: <b>1x = one full 5h / weekly window of a $20 Claude Pro account</b>
        (Max = 5x, Ultra = 10x, per claude.com/pricing). Each rise of an account's utilization is split across the users active on it
        (by cost) and follows the account's own reset. Quotas belong to subscriptions: copied from the plan on
        purchase, set here per subscription, or the group default below.
      </div>

      <div v-if="loading && !report" class="flex justify-center py-6">
        <Icon name="refresh" size="md" class="animate-spin text-primary-500" />
      </div>

      <template v-if="report">
        <!-- capacity + default -->
        <div class="grid gap-4 md:grid-cols-2">
          <div class="rounded-lg border border-gray-200 p-4 text-sm dark:border-dark-600">
            <div class="mb-2 text-xs font-medium text-gray-500">Pool capacity vs. allocated quotas</div>
            <div v-for="w in windows" :key="w" class="flex items-baseline gap-2">
              <span class="w-14 text-gray-500">{{ w === '7d' ? 'Weekly' : '5-hour' }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatX(cap(w), 1) }}</span>
              <span class="text-gray-500">capacity ·</span>
              <span :class="alloc(w) > cap(w) ? 'font-medium text-amber-600 dark:text-amber-400' : 'text-gray-900 dark:text-white'">
                {{ formatX(alloc(w), 1) }}
              </span>
              <span class="text-gray-500">allocated</span>
              <span v-if="unlimited(w)" class="text-xs text-gray-400">(+{{ unlimited(w) }} unlimited)</span>
              <span v-if="w === '5h' && report.accounts_without_5h_limit" class="text-xs text-gray-400">
                · +{{ report.accounts_without_5h_limit }} account{{ report.accounts_without_5h_limit === 1 ? '' : 's' }} with no 5-hour limit
              </span>
            </div>
          </div>
          <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
            <div class="mb-2 text-xs font-medium text-gray-500">
              Default quota for subscriptions without their own (admin-assigned, redeem codes, plans without a quota)
            </div>
            <div class="flex flex-wrap items-center gap-2 text-sm">
              <span class="text-gray-500">5h</span>
              <input v-model="def.q5" type="number" min="0" step="0.1" placeholder="unlimited" class="hide-spinner input w-24" />
              <span class="text-gray-500">7d</span>
              <input v-model="def.q7" type="number" min="0" step="0.1" placeholder="unlimited" class="hide-spinner input w-24" />
              <label class="ml-1 flex items-center gap-1 text-xs text-gray-500">
                <input v-model="def.none" type="checkbox" /> no default
              </label>
              <button type="button" class="btn btn-primary btn-sm ml-auto px-3 py-1" :disabled="busy || defInvalid" @click="saveDefault">
                Save
              </button>
            </div>
            <div v-if="defInvalid" class="mt-1 text-xs text-red-600 dark:text-red-400">Quotas must be ≥ 0.</div>
          </div>
        </div>

        <!-- subscriptions -->
        <div class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400">
                <th class="px-3 py-2">Subscription</th>
                <th class="px-3 py-2">5h used / quota</th>
                <th class="px-3 py-2">7d used / quota</th>
                <th class="px-3 py-2">Own quota (5h / 7d)</th>
                <th class="px-3 py-2">Status</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr v-if="!report.subscriptions.length">
                <td colspan="5" class="px-3 py-4 text-center text-xs text-gray-400">No active subscriptions in this group.</td>
              </tr>
              <tr v-for="s in report.subscriptions" :key="s.subscription_id ?? s.user_id">
                <td class="px-3 py-2">
                  <div class="text-gray-900 dark:text-white">{{ s.username || s.email }}</div>
                  <div class="text-xs text-gray-400">
                    {{ quotaSourceLabel(s.quota_source) }}<span v-if="s.expires_at"> · until {{ formatDate(s.expires_at) }}</span>
                  </div>
                </td>
                <td class="px-3 py-2"><ForkUsageShareBar :used="s.used_5h_x" :limit="s.quota_5h_x" :reset-at="s.next_drop_5h" /></td>
                <td class="px-3 py-2"><ForkUsageShareBar :used="s.used_7d_x" :limit="s.quota_7d_x" :reset-at="s.next_drop_7d" /></td>
                <td class="px-3 py-2">
                  <div v-if="s.subscription_id !== null" class="flex items-center gap-1">
                    <input v-model="subEdits[s.subscription_id].q5" type="number" min="0" step="0.1" placeholder="∞" class="hide-spinner input w-16" />
                    <input v-model="subEdits[s.subscription_id].q7" type="number" min="0" step="0.1" placeholder="∞" class="hide-spinner input w-16" />
                    <button type="button" class="btn btn-sm px-2 py-1" :disabled="busy || subInvalid(s.subscription_id)" @click="saveSub(s.subscription_id)">Set</button>
                    <button
                      v-if="s.quota_source === 'plan' || s.quota_source === 'admin'"
                      type="button"
                      class="btn btn-sm px-2 py-1"
                      title="Remove the subscription's own quota and use the group default"
                      :disabled="busy"
                      @click="clearSub(s.subscription_id)"
                    >
                      Default
                    </button>
                  </div>
                </td>
                <td class="whitespace-nowrap px-3 py-2 text-xs">
                  <span v-if="s.blocked" class="text-red-600 dark:text-red-400">blocked</span>
                  <span v-else-if="s.frees_at" class="text-amber-600 dark:text-amber-400">over · spare only</span>
                  <span v-else class="text-green-600 dark:text-green-400">ok</span>
                  <div v-if="s.frees_at" class="text-gray-400">frees {{ formatTime(s.frees_at) }}</div>
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
                <th class="px-3 py-2">Size (5h / 7d)</th>
                <th class="px-3 py-2">5h window</th>
                <th class="px-3 py-2">7d window</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr v-for="a in report.accounts" :key="a.id">
                <td class="px-3 py-2">
                  <div class="text-gray-900 dark:text-white">{{ a.name }}</div>
                  <div class="text-xs text-gray-400" :title="a.detect_error ?? ''">
                    {{ tierLabel(a.tier) }}<span v-if="a.detect_error" class="text-amber-600 dark:text-amber-400"> · detection failed</span>
                    <button
                      v-if="a.type === 'oauth'"
                      type="button"
                      class="ml-1 text-primary-600 hover:underline dark:text-primary-400"
                      :disabled="busy"
                      @click="redetect(a.id)"
                    >
                      re-detect
                    </button>
                  </div>
                </td>
                <td class="px-3 py-2">
                  <div class="flex items-center gap-1">
                    <input v-model="weightEdits[a.id].w5" type="number" min="0" step="1" :placeholder="String(autoWeight(a, '5h'))" class="hide-spinner input w-16" />
                    <input v-model="weightEdits[a.id].w7" type="number" min="0" step="1" :placeholder="String(autoWeight(a, '7d'))" class="hide-spinner input w-16" />
                    <button type="button" class="btn btn-sm px-2 py-1" :disabled="busy || weightInvalid(a.id)" @click="saveWeights(a.id)">Set</button>
                  </div>
                  <div class="mt-0.5 text-xs text-gray-400">
                    using {{ formatX(a.weight_5h, 1) }} / {{ formatX(a.weight_7d, 1) }}
                  </div>
                </td>
                <td class="px-3 py-2">
                  <span v-if="a.no_5h_limit" class="text-xs text-gray-500">no 5-hour limit (Ultra)</span>
                  <AccountWindow v-else :util="a.util_5h_pct" :reset="a.reset_5h" :weight="a.weight_5h" :by-sub="a.by_subscription_5h_x" :other="a.other_5h_x" :outside="a.outside_5h_x" />
                </td>
                <td class="px-3 py-2">
                  <AccountWindow :util="a.util_7d_pct" :reset="a.reset_7d" :weight="a.weight_7d" :by-sub="a.by_subscription_7d_x" :other="a.other_7d_x" :outside="a.outside_7d_x" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- global spare rule -->
        <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <div class="mb-2 text-xs font-medium text-gray-500">
            All groups: over its quota, a subscription may still use an account that has at least … left and resets within …
          </div>
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <span class="w-8 text-gray-500">5h</span>
            <input v-model="spare.min5" type="number" min="0" max="100" class="hide-spinner input w-20" />%
            <input v-model="spare.within5" type="number" min="0" class="hide-spinner input w-20" />min
            <span class="ml-3 w-8 text-gray-500">7d</span>
            <input v-model="spare.min7" type="number" min="0" max="100" class="hide-spinner input w-20" />%
            <input v-model="spare.within7" type="number" min="0" class="hide-spinner input w-20" />h
            <button type="button" class="btn btn-sm ml-auto px-3 py-1" :disabled="busy || spareInvalid" @click="saveSpare">Save</button>
          </div>
        </div>
      </template>

      <div class="flex items-center justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <button type="button" class="btn btn-sm px-4 py-1.5" :disabled="loading" @click="load">
          <Icon v-if="loading" name="refresh" size="sm" class="mr-1 animate-spin" />
          Refresh
        </button>
        <button type="button" class="btn btn-sm px-4 py-1.5" @click="emit('close')">Close</button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, reactive, ref, watch, type PropType } from 'vue'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'
import {
  formatX,
  getUsageShare,
  parseQuotaInput,
  quotaSourceLabel,
  setAccountWeights,
  setGroupDefaultQuota,
  setSubscriptionQuota,
  setUsageQuotaSettings,
  tierLabel,
  type UsageShareAccount,
  type UsageShareReport
} from '@/api/admin/forkUsageShare'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import ForkUsageShareBar from '@/components/admin/user/ForkUsageShareBar.vue'

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
}>()

type NumInput = number | string | null | undefined
type Win = '5h' | '7d'
const windows: Win[] = ['5h', '7d']

const appStore = useAppStore()
const loading = ref(false)
const busy = ref(false)
const report = ref<UsageShareReport | null>(null)
const def = reactive({ q5: null as NumInput, q7: null as NumInput, none: true })
const spare = reactive({ min5: 40 as NumInput, within5: 60 as NumInput, min7: 40 as NumInput, within7: 24 as NumInput })
const subEdits = ref<Record<number, { q5: NumInput; q7: NumInput }>>({})
const weightEdits = ref<Record<number, { w5: NumInput; w7: NumInput }>>({})

const cap = (w: Win) => (w === '5h' ? report.value?.capacity_5h_x : report.value?.capacity_7d_x) ?? 0
const alloc = (w: Win) => (w === '5h' ? report.value?.allocated_5h_x : report.value?.allocated_7d_x) ?? 0
const unlimited = (w: Win) => (w === '5h' ? report.value?.unlimited_5h : report.value?.unlimited_7d) ?? 0

const defInvalid = computed(() => !def.none && (parseQuotaInput(def.q5) === undefined || parseQuotaInput(def.q7) === undefined))
const spareInvalid = computed(
  () =>
    [parseQuotaInput(spare.min5, 100), parseQuotaInput(spare.min7, 100), parseQuotaInput(spare.within5), parseQuotaInput(spare.within7)].some(
      v => v === undefined || v === null
    )
)
const subInvalid = (id: number) => {
  const e = subEdits.value[id]
  return !e || parseQuotaInput(e.q5) === undefined || parseQuotaInput(e.q7) === undefined
}
const parseWeight = (v: NumInput): number | null | undefined => {
  if (v === null || v === undefined || v === '') return null
  const n = Number(v)
  return Number.isFinite(n) && n > 0 && n <= 1000 ? n : undefined
}
const weightInvalid = (id: number) => {
  const e = weightEdits.value[id]
  return !e || parseWeight(e.w5) === undefined || parseWeight(e.w7) === undefined
}
const autoWeight = (a: UsageShareAccount, w: Win) => {
  const override = w === '5h' ? a.weight_5h_override : a.weight_7d_override
  const used = w === '5h' ? a.weight_5h : a.weight_7d
  return override === null ? used : ({ pro: 1, max_5x: 5, max_20x: 10 } as Record<string, number>)[a.tier ?? ''] ?? 1
}

const formatTime = (iso: string | null | undefined) =>
  iso ? new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' }) : '—'
const formatDate = (iso: string) => new Date(iso).toLocaleDateString()

const applyReport = (r: UsageShareReport) => {
  report.value = r
  def.none = r.default === null
  def.q5 = r.default?.quota_5h_x ?? null
  def.q7 = r.default?.quota_7d_x ?? null
  spare.min5 = r.settings.spare_5h_min_pct
  spare.within5 = r.settings.spare_5h_within_minutes
  spare.min7 = r.settings.spare_7d_min_pct
  spare.within7 = r.settings.spare_7d_within_hours
  subEdits.value = Object.fromEntries(
    r.subscriptions
      .filter(s => s.subscription_id !== null)
      .map(s => [s.subscription_id as number, { q5: s.quota_5h_x, q7: s.quota_7d_x }])
  )
  weightEdits.value = Object.fromEntries(r.accounts.map(a => [a.id, { w5: a.weight_5h_override, w7: a.weight_7d_override }]))
}

const load = async () => {
  if (!props.group) return
  loading.value = true
  try {
    applyReport(await getUsageShare(props.group.id))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Failed to load usage quotas'))
  } finally {
    loading.value = false
  }
}

const run = async (action: () => Promise<unknown>, ok: string) => {
  busy.value = true
  try {
    await action()
    appStore.showSuccess(ok)
    await load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Save failed'))
  } finally {
    busy.value = false
  }
}

const saveDefault = () =>
  props.group &&
  run(
    () =>
      setGroupDefaultQuota(
        props.group!.id,
        def.none ? null : { quota_5h_x: parseQuotaInput(def.q5) ?? null, quota_7d_x: parseQuotaInput(def.q7) ?? null }
      ),
    'Default quota saved'
  )

const saveSub = (id: number) => {
  const e = subEdits.value[id]
  return run(
    () => setSubscriptionQuota(id, { quota_5h_x: parseQuotaInput(e.q5) ?? null, quota_7d_x: parseQuotaInput(e.q7) ?? null }),
    'Subscription quota saved'
  )
}

const clearSub = (id: number) => run(() => setSubscriptionQuota(id, null), 'Subscription now uses the group default')

const saveWeights = (id: number) => {
  const e = weightEdits.value[id]
  return run(
    () => setAccountWeights(id, { weight_5h_override: parseWeight(e.w5) ?? null, weight_7d_override: parseWeight(e.w7) ?? null }),
    'Account size saved'
  )
}

const redetect = (id: number) => {
  const e = weightEdits.value[id]
  return run(
    () =>
      setAccountWeights(id, {
        weight_5h_override: parseWeight(e?.w5) ?? null,
        weight_7d_override: parseWeight(e?.w7) ?? null,
        redetect: true
      }),
    'Re-detection queued'
  )
}

const saveSpare = () =>
  run(
    () =>
      setUsageQuotaSettings({
        spare_5h_min_pct: Number(spare.min5),
        spare_5h_within_minutes: Math.round(Number(spare.within5)),
        spare_7d_min_pct: Number(spare.min7),
        spare_7d_within_hours: Math.round(Number(spare.within7))
      }),
    'Spare-capacity rule saved'
  )

const subName = (id: string) => {
  const s = report.value?.subscriptions.find(x => String(x.subscription_id) === id)
  return s ? s.username || s.email : `#${id}`
}

const AccountWindow = defineComponent({
  props: {
    util: { type: Number as PropType<number | null>, default: null },
    reset: { type: String as PropType<string | null>, default: null },
    weight: { type: Number, required: true },
    bySub: { type: Object as PropType<Record<string, number>>, required: true },
    other: { type: Number, default: 0 },
    outside: { type: Number, default: 0 }
  },
  setup(p) {
    return () => {
      if (p.util === null) return h('span', { class: 'text-xs text-gray-400' }, 'no data')
      const parts = Object.entries(p.bySub)
        .sort((a, b) => b[1] - a[1])
        .map(([id, x]) => `${subName(id)} ${formatX(x)}`)
      if (p.other > 0.005) parts.push(`other groups ${formatX(p.other)}`)
      if (p.outside > 0.005) parts.push(`outside ${formatX(p.outside)}`)
      return h('div', { class: 'text-xs' }, [
        h(
          'div',
          { class: 'text-gray-900 dark:text-white' },
          `${formatX((p.util / 100) * p.weight)} of ${formatX(p.weight, 1)} (${p.util.toFixed(0)}%) · resets ${formatTime(p.reset)}`
        ),
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
