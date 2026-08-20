<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodeRefreshDialog. v0.8.7 ships the refresh-agent-bearer
  dialog for /api/v1/nodes/{id}/refresh-agent-bearer.
  Extracted from NodesView.vue in v0.9.x to keep the
  view's per-action dialogs in separate, focused files.
  Self-contained: owns its own loading/result/error state,
  the fire-and-forget POST on dialog open, and the success
  card with the new bearer + stored-key fingerprint.

  Lifecycle:
    * The parent sets `node` + flips `open` -> the
      dialog's watch hydrates / resets the loading state
      and clears any stale result/error from a previous
      open. The POST fires automatically (fire-and-forget)
      when the watcher sees `props.open` flip to true with
      a non-null node.
    * On 200: stash the response in `refreshResult`, hide
      the spinner, emit `refreshed` (with the row) so the
      parent can toast. The dialog stays open so the
      operator can copy the new bearer.
    * On 4xx/5xx: stash the error message in
      `refreshError`, hide the spinner, show inline error,
      emit `failed` (with the row + error message) so the
      parent can toast a "destructive" variant. The dialog
      stays open with the error so the operator can
      diagnose.
    * On cancel / ESC / backdrop click / Close button
      the dialog emits `update:open` -> false. The parent
      flips its `refreshOpen` ref.

  List refresh: the row's state machine did not change on
  refresh (only the agent bearer changed; the row's
  state is independent of the agent's auth). The parent
  does NOT refresh the list on `refreshed` — the success
  card is the only post-refresh surface.

  v0.9.x minor visual fix: the previous v0.8.7 markup
  referenced `.nodes__refresh-*` classes that were never
  defined in NodesView's style block (pre-existing
  oversight). This dialog colocates the rules so the
  loading / error / result surfaces style correctly;
  the styling mirrors the inspect dialog's loading /
  error rules for visual consistency.
-->
<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { RefreshCw } from "lucide-vue-next";

import { refreshNodeAgentBearer } from "@/api/services";
import { toApiError } from "@/api/client";
import type { Node, NodeRefreshAgentBearerResponse } from "@/types";

import Button from "@/components/ui/Button.vue";
import Dialog from "@/components/ui/Dialog.vue";
import DialogContent from "@/components/ui/DialogContent.vue";
import DialogHeader from "@/components/ui/DialogHeader.vue";
import DialogTitle from "@/components/ui/DialogTitle.vue";
import DialogDescription from "@/components/ui/DialogDescription.vue";
import DialogFooter from "@/components/ui/DialogFooter.vue";
import FormField from "@/components/FormField.vue";
import Input from "@/components/ui/Input.vue";

const props = defineProps<{
  open: boolean;
  node: Node | null;
}>();

const emit = defineEmits<{
  "update:open": [value: boolean];
  refreshed: [node: Node];
  failed: [node: Node, error: string];
}>();

const { t } = useI18n();

// Internal state. Owned by the dialog; the view
// never reads these. Reset every time the dialog
// opens so a stale result/error from a prior open
// does not leak across nodes / sessions.
const refreshResult = ref<NodeRefreshAgentBearerResponse | null>(null);
const refreshError = ref<string | null>(null);
const refreshLoading = ref(false);

// Fire-and-forget the POST on dialog open. The
// dialog shows a spinner while the SSH + cat
// runs; the success card lands when the Service
// returns. The catch path surfaces the error
// inline + emits `failed` so the parent can
// toast. A 409 "no stored key" response carries
// a "rotate-panel-key first" hint from the
// panel; a 502 (SSH connect / run / agent.env
// parse) carries a specific stage name. Both
// surface verbatim in the error block.
watch(
  () => [props.open, props.node] as const,
  ([isOpen, node]) => {
    if (isOpen && node) {
      refreshResult.value = null;
      refreshError.value = null;
      refreshLoading.value = true;
      refreshNodeAgentBearer(node.id, {})
        .then((res) => {
          refreshResult.value = res;
          refreshLoading.value = false;
          emit("refreshed", node);
        })
        .catch((error: unknown) => {
          const apiErr = toApiError(error);
          refreshError.value = apiErr.message;
          refreshLoading.value = false;
          emit("failed", node, apiErr.message);
        });
    }
  },
  { immediate: true },
);

function onOpenChange(value: boolean): void {
  emit("update:open", value);
}

function onClose(): void {
  // Closing the success card flips `open` back
  // to false; the parent clears its `refreshing`
  // pointer in its own `update:open` handler.
  emit("update:open", false);
}
</script>

<template>
  <Dialog
    :open="open"
    @update:open="onOpenChange"
  >
    <DialogContent>
      <DialogHeader>
        <DialogTitle>
          <RefreshCw class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.refreshTitle") }}
        </DialogTitle>
        <DialogDescription>
          {{
            t("nodes.refreshDescription")
          }}
        </DialogDescription>
      </DialogHeader>
      <p class="nodes__provision-target">
        <strong>{{ node?.name }}</strong>
        ({{ node?.address }})
        — {{ node ? t(`nodes.states.${node.state}`) : "" }}
      </p>
      <!-- Loading state. The dialog opens
           immediately (so the user can see
           the target node); the POST fires
           on open. The spinner is the only
           content until the response lands.
           The shape mirrors the inspect
           dialog's loading surface for visual
           consistency. -->
      <div
        v-if="refreshLoading && !refreshResult && !refreshError"
        class="nodes__refresh-loading"
      >
        {{ t("nodes.refreshLoading") }}
      </div>
      <!-- Error state. The toast was already
           shown by the parent handler; the
           dialog shows a brief inline error
           so the user knows the dialog content
           is in a failed state. The 409 "no
           stored key" case carries a "rotate-
           panel-key first" hint from the panel;
           the operator sees the full error
           message verbatim. The 502 cases
           (SSH connect / run / agent.env parse)
           carry a specific stage name. -->
      <div
        v-else-if="refreshError"
        class="nodes__refresh-error"
      >
        {{ refreshError }}
      </div>
      <!-- Success card. Renders after a 200
           response. The dialog stays open so
           the operator can copy the new bearer
           before closing. The new bearer is
           the AEGIS_AGENT_BEARER value from
           /etc/aegis/agent.env on the node;
           the fingerprint is the SHA-256 of
           the stored panel key (proves "the
           refresh used the key I expect"). -->
      <div
        v-else
        class="nodes__refresh-result"
      >
        <h3 class="nodes__refresh-result-title">
          <RefreshCw class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.refreshResultTitle") }}
        </h3>
        <p class="nodes__refresh-result-help">
          {{ t("nodes.refreshResultHelp") }}
        </p>
        <FormField
          name="refresh-bearer"
          :label="t('nodes.refreshBearer')"
        >
          <template #default="{ id }">
            <Input
              :id="id"
              :model-value="refreshResult?.bearer ?? ''"
              readonly
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
        <FormField
          name="refresh-fingerprint"
          :label="t('nodes.refreshFingerprint')"
        >
          <template #default="{ id }">
            <Input
              :id="id"
              :model-value="refreshResult?.key_fingerprint ?? ''"
              readonly
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
        <DialogFooter>
          <Button
            type="button"
            @click="onClose"
          >
            {{ t("common.close") }}
          </Button>
        </DialogFooter>
      </div>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
/* v0.9.x: colocation with the dialog markup. The
   previous v0.8.7 markup referenced these classes
   but never defined them (pre-existing oversight);
   the rules mirror the inspect dialog's loading /
   error styling for visual consistency. */
.nodes__refresh-loading {
  padding: 0.75rem 1rem;
  border-radius: 0.5rem;
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
  text-align: center;
  font-size: 0.9rem;
  margin: 0.5rem 0;
}

.nodes__refresh-error {
  padding: 0.75rem 1rem;
  border-radius: 0.5rem;
  background: hsl(var(--destructive) / 0.1);
  color: hsl(var(--destructive));
  font-family: monospace;
  font-size: 0.8rem;
  margin: 0.5rem 0;
}

.nodes__refresh-result {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin-top: 0.5rem;
}

.nodes__refresh-result-title {
  font-size: 0.95rem;
  font-weight: 600;
  margin: 0;
}

.nodes__refresh-result-help {
  font-size: 0.85rem;
  color: hsl(var(--muted-foreground));
  margin: 0;
}

/* v0.9.x: colocation with the dialog markup. The
   same `.nodes__provision-target` style still
   lives in NodesView.vue for any future per-action
   dialogs (and is duplicated in NodeProvisionDialog
   + NodeRotateDialog for self-containment). */
.nodes__provision-target {
  margin: 0 0 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  font-size: 0.875rem;
}
</style>
