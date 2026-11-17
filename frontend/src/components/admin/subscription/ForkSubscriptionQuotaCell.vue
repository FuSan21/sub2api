<template>
  <!-- [fork] Usage share column on the admin Subscriptions page: usage vs. quota in x, editable -->
  <div class="min-w-[220px]">
    <template v-if="status">
      <div class="space-y-1">
        <ForkUsageShareBar label="5h" :used="status.used_5h_x" :limit="status.quota_5h_x" :capacity="status.capacity_5h_x" :reset-at="status.next_drop_5h" color="indigo" />
        <ForkUsageShareBar label="7d" :used="status.used_7d_x" :limit="status.quota_7d_x" :capacity="status.capacity_7d_x" :reset-at="status.next_drop_7d" color="emerald" />
      </div>
      <div class="mt-0.5 flex items-center gap-1 text-[10px]">
        <span v-if="status.blocked" class="font-medium text-red-600 dark:text-red-400">blocked ·</span>
        <span v-else-if="status.frees_at" class="font-medium text-amber-600 dark:text-amber-400">over quota ·</span>
        <span class="text-gray-400">{{ quotaSourceLabel(status.quota_source) }}</span>
        <button type="button" class="text-primary-600 hover:underline dark:text-primary-400" @click="startEdit">edit</button>
      </div>
    </template>
    <span v-else class="text-xs text-gray-400">—</span>

    <div v-if="editing" class="mt-1 flex flex-wrap items-center gap-1">
      <input v-model="q5" type="number" min="0" step="0.1" placeholder="5h ∞" class="hide-spinner input h-7 w-16 px-1.5 text-xs" />
      <input v-model="q7" type="number" min="0" step="0.1" placeholder="7d ∞" class="hide-spinner input h-7 w-16 px-1.5 text-xs" />
      <button type="button" class="btn btn-primary btn-sm px-2 py-0.5 text-xs" :disabled="busy || invalid" @click="save(false)">Set</button>
      <button type="button" class="btn btn-sm px-2 py-0.5 text-xs" title="Use the group default" :disabled="busy" @click="save(true)">Default</button>
      <button type="button" class="btn btn-sm px-2 py-0.5 text-xs" :disabled="busy" @click="editing = false">✕</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAppStore } from '@/stores/app'
import { parseQuotaInput, quotaSourceLabel, setSubscriptionQuota } from '@/api/admin/forkUsageShare'
import { useForkUsageShareStatuses } from '@/composables/forkUsageShareStatuses'
import { extractApiErrorMessage } from '@/utils/apiError'
import ForkUsageShareBar from '@/components/admin/user/ForkUsageShareBar.vue'

const props = defineProps<{ subscriptionId: number }>()

type NumInput = number | string | null | undefined
const appStore = useAppStore()
const { statuses, ensureLoaded } = useForkUsageShareStatuses()
void ensureLoaded()

const status = computed(() => statuses.value.find(s => s.subscription_id === props.subscriptionId) ?? null)
const editing = ref(false)
const busy = ref(false)
const q5 = ref<NumInput>(null)
const q7 = ref<NumInput>(null)
const invalid = computed(() => parseQuotaInput(q5.value) === undefined || parseQuotaInput(q7.value) === undefined)

const startEdit = () => {
  q5.value = status.value?.quota_5h_x ?? null
  q7.value = status.value?.quota_7d_x ?? null
  editing.value = true
}

const save = async (clear: boolean) => {
  busy.value = true
  try {
    await setSubscriptionQuota(
      props.subscriptionId,
      clear ? null : { quota_5h_x: parseQuotaInput(q5.value) ?? null, quota_7d_x: parseQuotaInput(q7.value) ?? null }
    )
    editing.value = false
    await ensureLoaded(true)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Failed to save quota'))
  } finally {
    busy.value = false
  }
}
</script>
