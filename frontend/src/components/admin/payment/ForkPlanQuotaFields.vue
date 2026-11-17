<template>
  <!-- [fork] Usage share: the quota a plan grants, copied onto the subscription on purchase -->
  <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
    <label class="input-label">Usage quota (Pro-equivalents)</label>
    <div class="flex flex-wrap items-center gap-2 text-sm">
      <span class="text-gray-500">5h</span>
      <input v-model="q5" type="number" min="0" step="0.1" placeholder="unlimited" class="hide-spinner input w-28" :disabled="useDefault" />
      <span class="text-gray-500">7d</span>
      <input v-model="q7" type="number" min="0" step="0.1" placeholder="unlimited" class="hide-spinner input w-28" :disabled="useDefault" />
      <label class="flex items-center gap-1 text-xs text-gray-500">
        <input v-model="useDefault" type="checkbox" /> use the group default
      </label>
    </div>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
      1x = the full 5-hour / weekly limit of one Claude Pro ($20) account. Applied to the buyer's subscription on each
      purchase or renewal; existing subscriptions keep their current quota.
    </p>
    <p v-if="invalid" class="mt-1 text-xs text-red-600 dark:text-red-400">Quotas must be ≥ 0.</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { getPlanQuotas, parseQuotaInput, setPlanQuota } from '@/api/admin/forkUsageShare'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  show: boolean
  planId: number | null
}>()

type NumInput = number | string | null | undefined
const appStore = useAppStore()
const q5 = ref<NumInput>(null)
const q7 = ref<NumInput>(null)
const useDefault = ref(true)
const initial = ref<string>('')

const invalid = computed(() => !useDefault.value && (parseQuotaInput(q5.value) === undefined || parseQuotaInput(q7.value) === undefined))
const snapshot = () => JSON.stringify([useDefault.value, parseQuotaInput(q5.value), parseQuotaInput(q7.value)])

watch(
  () => [props.show, props.planId] as const,
  async ([show, planId]) => {
    if (!show) return
    q5.value = null
    q7.value = null
    useDefault.value = true
    if (planId) {
      try {
        const q = (await getPlanQuotas())[String(planId)]
        if (q) {
          useDefault.value = false
          q5.value = q.quota_5h_x
          q7.value = q.quota_7d_x
        }
      } catch {
        // engine unavailable: leave the fields empty
      }
    }
    initial.value = snapshot()
  },
  { immediate: true }
)

/** Saves the quota after the plan itself was saved. New plans are looked up by name and group. */
async function save(planId: number | null, form: { name: string; group_id: number | null }) {
  if (invalid.value) {
    appStore.showError('Plan saved, but its usage quota is invalid and was not saved')
    return
  }
  if (planId && snapshot() === initial.value) return
  if (!planId && useDefault.value) return
  try {
    let id = planId
    if (!id) {
      const { data } = await adminPaymentAPI.getPlans()
      const match = (data ?? []).filter(p => p.name === form.name && p.group_id === form.group_id).sort((a, b) => b.id - a.id)[0]
      if (!match) throw new Error('new plan not found')
      id = match.id
    }
    await setPlanQuota(
      id,
      useDefault.value ? null : { quota_5h_x: parseQuotaInput(q5.value) ?? null, quota_7d_x: parseQuotaInput(q7.value) ?? null }
    )
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Plan saved, but its usage quota was not'))
  }
}

defineExpose({ save, invalid })
</script>
