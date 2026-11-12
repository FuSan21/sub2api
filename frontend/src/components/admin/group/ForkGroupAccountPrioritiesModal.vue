<template>
  <!-- [fork] Per-group account priority overrides -->
  <BaseDialog :show="show" title="Account priority" width="wide" @close="emit('close')">
    <div v-if="group" class="space-y-4">
      <div class="rounded-lg bg-gray-50 px-4 py-2.5 text-sm text-gray-600 dark:bg-dark-700 dark:text-gray-400">
        <span class="font-medium text-gray-900 dark:text-white">{{ group.name }}</span>
        — lower number is preferred; equal numbers share load. Leave the override empty to use the
        account's own priority. API keys in this group only use the accounts listed here.
      </div>

      <div v-if="loading" class="flex justify-center py-6">
        <Icon name="refresh" size="md" class="animate-spin text-primary-500" />
      </div>

      <div v-else-if="rows.length === 0" class="py-6 text-center text-sm text-gray-400 dark:text-gray-500">
        No accounts are bound to this group.
      </div>

      <div v-else class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-200 bg-gray-50 text-left text-xs font-medium text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400">
              <th class="px-3 py-2">Account</th>
              <th class="px-3 py-2">Status</th>
              <th class="px-3 py-2">Account priority</th>
              <th class="px-3 py-2">Group override</th>
              <th class="px-3 py-2">Effective</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
            <tr v-for="row in sortedRows" :key="row.account_id">
              <td class="px-3 py-2">
                <div class="flex items-center gap-2 text-gray-900 dark:text-white">
                  <PlatformIcon :platform="row.platform" size="sm" />
                  {{ row.name }}
                  <span class="text-xs text-gray-400">#{{ row.account_id }}</span>
                </div>
              </td>
              <td class="px-3 py-2 text-gray-600 dark:text-gray-400">
                {{ row.status }}<span v-if="!row.schedulable"> (paused)</span>
              </td>
              <td class="px-3 py-2 text-gray-600 dark:text-gray-400">{{ row.account_priority }}</td>
              <td class="px-3 py-2">
                <input
                  v-model="drafts[row.account_id]"
                  type="number"
                  step="1"
                  min="0"
                  autocomplete="off"
                  :placeholder="String(row.account_priority)"
                  class="hide-spinner input w-24 text-center"
                />
              </td>
              <td class="px-3 py-2 font-medium text-gray-900 dark:text-white">{{ effective(row) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="flex items-center justify-end gap-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <span v-if="invalid" class="mr-auto text-xs text-red-600 dark:text-red-400">
          Overrides must be whole numbers ≥ 0.
        </span>
        <button type="button" class="btn btn-sm px-4 py-1.5" @click="emit('close')">Close</button>
        <button
          type="button"
          class="btn btn-primary btn-sm px-4 py-1.5"
          :disabled="saving || loading || invalid || rows.length === 0"
          @click="save"
        >
          <Icon v-if="saving" name="refresh" size="sm" class="mr-1 animate-spin" />
          Save
        </button>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'
import {
  getGroupAccountPriorities,
  setGroupAccountPriorities,
  type GroupAccountPriority
} from '@/api/admin/forkGroupAccountPriorities'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
}>()

const appStore = useAppStore()
const loading = ref(false)
const saving = ref(false)
const rows = ref<GroupAccountPriority[]>([])
// v-model on number inputs yields number, or '' when cleared.
const drafts = ref<Record<number, number | string | null>>({})

const parseDraft = (value: number | string | null | undefined): number | null | undefined => {
  if (value === null || value === undefined || value === '') return null
  const n = Number(value)
  return Number.isInteger(n) && n >= 0 ? n : undefined
}

const invalid = computed(() => rows.value.some(r => parseDraft(drafts.value[r.account_id]) === undefined))

const effective = (row: GroupAccountPriority) => parseDraft(drafts.value[row.account_id]) ?? row.account_priority

const sortedRows = computed(() =>
  [...rows.value].sort((a, b) => a.effective_priority - b.effective_priority || a.account_id - b.account_id)
)

const applyRows = (items: GroupAccountPriority[]) => {
  rows.value = items
  drafts.value = Object.fromEntries(items.map(r => [r.account_id, r.override_priority]))
}

const load = async () => {
  if (!props.group) return
  loading.value = true
  try {
    applyRows(await getGroupAccountPriorities(props.group.id))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Failed to load account priorities'))
  } finally {
    loading.value = false
  }
}

const save = async () => {
  if (!props.group || invalid.value) return
  saving.value = true
  try {
    const items = rows.value.map(r => ({
      account_id: r.account_id,
      priority: parseDraft(drafts.value[r.account_id]) ?? null
    }))
    applyRows(await setGroupAccountPriorities(props.group.id, items))
    appStore.showSuccess('Account priorities saved')
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, 'Failed to save account priorities'))
  } finally {
    saving.value = false
  }
}

watch(
  () => [props.show, props.group?.id] as const,
  ([show]) => {
    if (show) void load()
  },
  { immediate: true }
)
</script>
