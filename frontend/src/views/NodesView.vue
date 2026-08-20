<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodesView. v0.1.0 ships the full CRUD surface
  against /api/v1/nodes:

    * list (DataTable)
    * create (NodeCreateDialog)
    * edit   (NodeEditDialog)
    * delete (DropdownMenu confirm)

  v0.3.0 lands: BYO Node provision — a third
  dialog with the SSH credentials + tofu policy,
  mounted from the per-row DropdownMenu. The
  backend route is `POST /api/v1/nodes/{id}/provision`
  (see `internal/bootstrap/handler.go`).

  v0.8.4 lands: rotate-panel-key — a fourth
  dialog with the operator's existing PEM,
  mounted from the per-row DropdownMenu. The
  backend route is
  `POST /api/v1/nodes/{id}/rotate-panel-key`
  (see `internal/bootstrap/handler.go`; HTTP
  mirror of the v0.8.3 `aegis admin node
  rotate-panel-key` CLI from PR #184). The
  dialog is hidden for `new` state (the panel
  cannot SSH into a never-installed node
  because no key is in authorized_keys);
  visible for `online`, `offline`, and
  `disabled` (the operator may still want to
  rotate a key on a node they later intend to
  re-enable).

  v0.9.x: the create + edit dialogs were
  extracted into ./dialogs/NodeCreateDialog.vue
  and ./dialogs/NodeEditDialog.vue. The view
  keeps the per-row DropdownMenu actions
  (Provision / Rotate / Refresh / Inspect /
  Delete) and the list state. The `editing`
  ref is renamed to `editingForEdit` so the
  Provision / Rotate / Refresh / Inspect flows
  can keep their own per-row refs without
  sharing the same slot.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { MoreHorizontal, Plus, Server } from "lucide-vue-next";
import { KeyRound, KeySquare, Lock, RefreshCw, Eye } from "lucide-vue-next";
import type { ColumnDef } from "@tanstack/vue-table";

import { useAuthStore } from "@/stores/auth";
import { useToastStore } from "@/stores/toast";
import { toApiError } from "@/api/client";
import {
  deleteNode,
  getStoredNodeKey,
  listNodes,
  provisionNode,
  refreshNodeAgentBearer,
  rotateNodePanelKey,
} from "@/api/services";
import type {
  Node,
  NodeRefreshAgentBearerResponse,
  NodeRotatePanelKeyResponse,
  NodeState,
  NodeStoredKey,
  UUID,
} from "@/types";
import {
  nodeProvisionSchema,
  nodeRotatePanelKeySchema,
} from "@/schemas";
import { useZodForm } from "@/composables/useZodForm";

import DataTable from "@/components/DataTable.vue";
import Button from "@/components/ui/Button.vue";
import Badge from "@/components/ui/Badge.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import Dialog from "@/components/ui/Dialog.vue";
import DialogContent from "@/components/ui/DialogContent.vue";
import DialogHeader from "@/components/ui/DialogHeader.vue";
import DialogTitle from "@/components/ui/DialogTitle.vue";
import DialogDescription from "@/components/ui/DialogDescription.vue";
import DialogFooter from "@/components/ui/DialogFooter.vue";
import DialogClose from "@/components/ui/DialogClose.vue";
import DropdownMenu from "@/components/ui/DropdownMenu.vue";
import DropdownMenuTrigger from "@/components/ui/DropdownMenuTrigger.vue";
import DropdownMenuContent from "@/components/ui/DropdownMenuContent.vue";
import DropdownMenuItem from "@/components/ui/DropdownMenuItem.vue";
import Input from "@/components/ui/Input.vue";
import Textarea from "@/components/ui/Textarea.vue";
import RadioGroup from "@/components/ui/RadioGroup.vue";
import RadioGroupItem from "@/components/ui/RadioGroupItem.vue";
import Form from "@/components/Form.vue";
import FormField from "@/components/FormField.vue";
import NodeCreateDialog from "./dialogs/NodeCreateDialog.vue";
import NodeEditDialog from "./dialogs/NodeEditDialog.vue";

const { t } = useI18n();
const auth = useAuthStore();
const toast = useToastStore();

const nodes = ref<Node[]>([]);
const loading = ref(false);
// v0.9.x: the create + edit dialogs each own
// their own form state. The view keeps the
// trigger refs (`createOpen` / `editOpen`)
// and the `editingForEdit` row pointer for
// the edit dialog. Provision / Rotate /
// Refresh / Inspect keep their own
// per-action refs (`provisioning`, `rotating`,
// `refreshing`, `inspecting`) so they can
// each render a per-row context card without
// sharing a single slot.
const createOpen = ref(false);
const editOpen = ref(false);
const editingForEdit = ref<Node | null>(null);

// v0.3.0: provision dialog. The "provisioning" node
// is the one currently being installed; the dialog
// stays open until the install completes (sync
// call) and shows the new state on success.
const provisioning = ref<Node | null>(null);
const provisionOpen = ref(false);

// v0.8.4: rotate-panel-key dialog. The "rotating"
// node is the one whose panel key is being
// regenerated. The dialog stays open until the
// SSH handshake + SFTP push + remote shell
// complete (sync call) and shows the new public
// key line + SHA256 fingerprint on success.
//
// We keep the response object on `rotationResult`
// so the success card can render the public key
// line + fingerprint side-by-side. The card
// stays in the dialog after the form closes so
// the operator can copy the fingerprint.
const rotating = ref<Node | null>(null);
const rotateOpen = ref(false);
const rotationResult = ref<NodeRotatePanelKeyResponse | null>(null);

// v0.8.5: stored-key dialog. The "inspecting"
// node is the one whose stored panel SSH key
// the operator wants to verify. The dialog
// stays open until the user closes it; the
// `inspection` ref holds the loaded
// `NodeStoredKey` surface (or `null` while
// loading / on error). The `inspectionLoading`
// ref drives the dialog's spinner; the
// `inspectionError` ref drives the error
// toast. The shape mirrors the rotate-panel-key
// dialog's lifecycle for visual consistency
// (both dialogs surface a read-only public-key
// surface to the operator).
const inspecting = ref<Node | null>(null);
const inspectOpen = ref(false);
const inspection = ref<NodeStoredKey | null>(null);
const inspectionLoading = ref(false);
const inspectionError = ref<string | null>(null);

// v0.8.7: refresh-agent-bearer dialog. The
// "refreshing" node is the one whose agent
// bearer the panel will re-fetch from the
// node. The dialog is a confirm-only flow
// (no per-call body fields are required;
// the ssh_port / ssh_user overrides are
// optional and the panel uses the node's
// stored Address + the service-wide
// AgentSSHUser when the body is empty).
// The success card carries the new bearer
// + the SHA-256 fingerprint of the stored
// panel key, so the operator can verify
// the refresh used the key they expect
// (same verification pattern as the
// v0.8.4 rotate-panel-key success card).
const refreshing = ref<Node | null>(null);
const refreshOpen = ref(false);
const refreshResult = ref<NodeRefreshAgentBearerResponse | null>(null);
const refreshError = ref<string | null>(null);
const refreshLoading = ref(false);

// Only `new` and `offline` are provisionable per
// ARCHITECTURE §8.3. The dropdown hides the entry
// for the other three so the operator does not
// see a 409 on click.
function isProvisionable(state: NodeState): boolean {
  return state === "new" || state === "offline";
}

// v0.8.4: rotate-panel-key is hidden for `new`
// (the panel has no key to back-fill — the
// `provision` dialog does the first-install
// flow on that path). For `online` / `offline`
// / `disabled` / `draining` the dropdown shows
// the entry: the operator pastes the PEM they
// used to install the node, the panel SSHes
// in, generates a new key, appends the public
// half to authorized_keys.
function isRotatable(state: NodeState): boolean {
  return state !== "new";
}

// v0.8.7: refresh-agent-bearer is the
// operator-side recovery path for the
// agent bearer. Same state gate as
// rotate-panel-key: hidden for `new`
// (no stored key to decrypt; the
// operator must install first). The
// action is independent of the panel
// key's existence — the node COULD
// have a stored key even if its
// state is `new` (e.g. a v0.8.3
// rotate-panel-key CLI ran on a
// never-online node), but the
// no-stored-key HTTP path returns 409
// with a "rotate-panel-key first"
// hint. Keeping the gate `!== "new"`
// matches the rotate-panel-key gate
// and avoids the operator seeing
// "Refresh agent bearer" on a never-
// installed row.
function isRefreshable(state: NodeState): boolean {
  return state !== "new";
}

async function refresh(): Promise<void> {
  loading.value = true;
  try {
    nodes.value = await listNodes();
  } catch (error) {
    const apiErr = toApiError(error);
    toast.add({
      title: t("nodes.loadFailed"),
      description: apiErr.message,
      variant: "destructive",
    });
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void refresh();
});

// --- Create (extracted to NodeCreateDialog in v0.9.x) ---------------------

function startCreate(): void {
  // The dialog owns its own form state and
  // resets the form every time it opens, so the
  // view just flips the trigger.
  createOpen.value = true;
}

function onNodeCreated(_node: Node): void {
  createOpen.value = false;
  void refresh();
}

// --- Edit (extracted to NodeEditDialog in v0.9.x) --------------------------

function startEdit(node: Node): void {
  // The dialog hydrates its form from the
  // `node` prop via a watcher, so the view just
  // sets the row pointer and opens.
  editingForEdit.value = node;
  editOpen.value = true;
}

function onNodeUpdated(_node: Node): void {
  editOpen.value = false;
  editingForEdit.value = null;
  void refresh();
}

// --- Provision (v0.3.0) ----------------------------------------------------

function startProvision(node: Node): void {
  provisioning.value = node;
  // v0.8.x: the auth-method radio default.
  // First-time installs (state 'new') default
  // to 'key' (the operator pastes a private
  // key OR a password; the form switches to
  // 'password' on the radio). Re-provisions
  // (state 'offline', a previously-provisioned
  // node) default to 'stored' — the panel
  // re-uses its own key, the operator clicks
  // submit with no input.
  const defaultAuth = node.state === "offline" ? "stored" : "key";
  provisionForm.resetForm({
    values: {
      authMethod: defaultAuth,
      ssh_port: undefined,
      ssh_user: "",
      ssh_private_key: "",
      ssh_password: "",
      tofu_policy: "reject",
      expected_fingerprint: "",
    },
  });
  provisionOpen.value = true;
}

const provisionForm = useZodForm({
  schema: nodeProvisionSchema,
  initialValues: {
    authMethod: "key" as const,
    ssh_port: undefined,
    ssh_user: "",
    ssh_private_key: "",
    ssh_password: "",
    tofu_policy: "reject" as const,
    expected_fingerprint: "",
  },
  onSubmit: async (values) => {
    if (!provisioning.value) return;
    // v0.8.x: the UI's authMethod radio is a
    // local form state; the Go API's wire
    // format is the two-field XOR
    // `ssh_private_key` / `ssh_password`. Build
    // the wire payload from the auth method:
    // - 'key'      -> { ssh_private_key }
    // - 'password' -> { ssh_password }
    // - 'stored'   -> {} (empty auth; the
    //   Go provisioner falls back to the
    //   encrypted panel key it stored on the
    //   first install).
    const wirePayload: {
      ssh_private_key?: string;
      ssh_password?: string;
      ssh_port?: number;
      ssh_user?: string;
      tofu_policy?: "reject" | "accept-and-append";
      expected_fingerprint?: string;
    } = {
      ssh_port: values.ssh_port,
      ssh_user: values.ssh_user,
      tofu_policy: values.tofu_policy,
      expected_fingerprint: values.expected_fingerprint,
    };
    if (values.authMethod === "key") {
      wirePayload.ssh_private_key = values.ssh_private_key;
    } else if (values.authMethod === "password") {
      wirePayload.ssh_password = values.ssh_password;
    }
    // 'stored' path: no auth fields. The Go side
    // falls back to the encrypted panel key.
    try {
      const res = await provisionNode(
        provisioning.value.id,
        wirePayload as Parameters<typeof provisionNode>[1],
      );
      provisionOpen.value = false;
      provisioning.value = null;
      toast.add({
        title: t("nodes.provisioned", { state: res.new_state }),
        variant: res.new_state === "online" ? "success" : "destructive",
      });
      await refresh();
    } catch (error) {
      toast.add({
        title: t("nodes.provisionFailed"),
        description: toApiError(error).message,
        variant: "destructive",
      });
    }
  },
});

// --- Rotate (v0.8.4) -------------------------------------------------------

function startRotate(node: Node): void {
  rotating.value = node;
  rotationResult.value = null;
  rotateForm.resetForm({
    values: {
      ssh_private_key: "",
      ssh_port: undefined,
      ssh_user: "",
    },
  });
  rotateOpen.value = true;
}

const rotateForm = useZodForm({
  schema: nodeRotatePanelKeySchema,
  initialValues: {
    ssh_private_key: "",
    ssh_port: undefined,
    ssh_user: "",
  },
  onSubmit: async (values) => {
    if (!rotating.value) return;
    try {
      const res = await rotateNodePanelKey(rotating.value.id, {
        ssh_private_key: values.ssh_private_key,
        ssh_port: values.ssh_port,
        ssh_user: values.ssh_user,
      });
      // Stash the response so the success card
      // can render the public key line +
      // fingerprint side-by-side. The dialog
      // stays open (rotateOpen stays true) so
      // the operator can copy the fingerprint
      // before closing — the form is "submitted"
      // but the success card is the closing
      // surface.
      rotationResult.value = res;
      toast.add({
        title: t("nodes.rotated", { name: rotating.value.name }),
        variant: "success",
      });
      // The list does not need a refresh —
      // the row's state machine did not
      // change; only the row's encrypted
      // ssh_private_key_ciphertext column
      // changed, and the panel does not
      // expose that field in the wire
      // shape (Node doesn't include it).
    } catch (error) {
      const apiErr = toApiError(error);
      toast.add({
        title: t("nodes.rotateFailed"),
        description: apiErr.message,
        variant: "destructive",
      });
    }
  },
});

function closeRotateDialog(): void {
  rotateOpen.value = false;
  rotating.value = null;
  rotationResult.value = null;
}

// --- Refresh agent bearer (v0.8.7) -------------------------------------

// v0.8.7: the refresh-agent-bearer flow is
// confirm-only (no per-call body fields are
// required). The dialog opens with a
// "this will SSH into the node and read
// /etc/aegis/agent.env" description; the
// operator clicks the confirm button and
// the panel does the rest. The success
// card carries the new bearer + the
// stored-key fingerprint for at-a-glance
// verification.
//
// The body the panel sends is empty by
// default (the panel uses the node's
// stored Address + the service-wide
// AgentSSHUser). A future PR can add
// per-call ssh_port / ssh_user override
// fields to the dialog; the schema is
// already in place (`nodeRefreshAgentBearerSchema`).
function startRefresh(node: Node): void {
  refreshing.value = node;
  refreshResult.value = null;
  refreshError.value = null;
  refreshOpen.value = true;
  refreshLoading.value = true;
  // Fire-and-forget. The dialog shows a
  // spinner while the SSH + cat runs;
  // the success card lands when the
  // Service returns. The catch path
  // surfaces a toast and keeps the
  // dialog open with the error
  // message so the operator can
  // diagnose (502 with a specific
  // "SSH connect" / "read agent.env" /
  // "parse agent.env" message is the
  // most common failure mode).
  refreshNodeAgentBearer(node.id, {})
    .then((res) => {
      refreshResult.value = res;
      refreshLoading.value = false;
      toast.add({
        title: t("nodes.refreshed", { name: node.name }),
        variant: "success",
      });
    })
    .catch((error: unknown) => {
      const apiErr = toApiError(error);
      refreshError.value = apiErr.message;
      refreshLoading.value = false;
      toast.add({
        title: t("nodes.refreshFailed"),
        description: apiErr.message,
        variant: "destructive",
      });
    });
}

function closeRefreshDialog(): void {
  refreshOpen.value = false;
  refreshing.value = null;
  refreshResult.value = null;
  refreshError.value = null;
  refreshLoading.value = false;
}

// --- Inspect stored key (v0.8.5) ----------------------------------------

function startInspect(node: Node): void {
  inspecting.value = node;
  inspection.value = null;
  inspectionError.value = null;
  inspectionLoading.value = true;
  inspectOpen.value = true;
  // Fire-and-forget the GET. The dialog's
  // spinner is driven by `inspectionLoading`;
  // the result (or error) lands in the
  // `inspection` / `inspectionError` refs.
  // The pattern matches `getCredentialsByUser`
  // in CredentialsView.vue — a one-shot fetch
  // on dialog open, no polling, no
  // refetch-on-error (the operator can close
  // and re-open to retry).
  void loadStoredKey(node.id);
}

async function loadStoredKey(id: UUID): Promise<void> {
  try {
    const sk = await getStoredNodeKey(id);
    inspection.value = sk;
  } catch (error) {
    const apiErr = toApiError(error);
    inspectionError.value = apiErr.message;
    toast.add({
      title: t("nodes.inspectFailed"),
      description: apiErr.message,
      variant: "destructive",
    });
  } finally {
    inspectionLoading.value = false;
  }
}

function closeInspectDialog(): void {
  inspectOpen.value = false;
  inspecting.value = null;
  inspection.value = null;
  inspectionError.value = null;
  inspectionLoading.value = false;
}

// --- Delete -----------------------------------------------------------------

const deleteConfirmOpen = ref(false);
const pendingDelete = ref<Node | null>(null);

async function confirmDelete(node: Node): Promise<void> {
  pendingDelete.value = node;
  deleteConfirmOpen.value = true;
}

async function performDelete(): Promise<void> {
  const target = pendingDelete.value;
  if (!target) return;
  pendingDelete.value = null;
  try {
    await deleteNode(target.id);
    toast.add({ title: t("nodes.deleted"), variant: "success" });
    await refresh();
  } catch (error) {
    toast.add({
      title: t("nodes.deleteFailed"),
      description: toApiError(error).message,
      variant: "destructive",
    });
  }
}

// --- Table columns ----------------------------------------------------------

const stateVariant: Record<
  NodeState,
  "default" | "success" | "warning" | "destructive" | "secondary"
> = {
  new: "secondary",
  online: "success",
  draining: "warning",
  offline: "destructive",
  disabled: "secondary",
};

const columns: ColumnDef<Node, unknown>[] = [
  { accessorKey: "name", header: () => t("nodes.name") },
  { accessorKey: "region", header: () => t("nodes.region") },
  {
    accessorKey: "state",
    header: () => t("nodes.state"),
    cell: ({ row }) =>
      h(Badge, { variant: stateVariant[row.original.state] }, () =>
        t(`nodes.states.${row.original.state}`),
      ),
  },
  { accessorKey: "address", header: () => t("nodes.address") },
  {
    id: "actions",
    header: () => h("span", { class: "sr-only" }, "Actions"),
    cell: ({ row }) =>
      h(DropdownMenu, null, () => [
        h(DropdownMenuTrigger, null, () =>
          h(
            Button,
            {
              variant: "ghost",
              size: "icon",
              "aria-label": t("common.actions"),
            },
            () => h(MoreHorizontal, { class: "h-4 w-4" }),
          ),
        ),
        h(DropdownMenuContent, { align: "end" }, () => [
          h(DropdownMenuItem, { onSelect: () => startEdit(row.original) }, () =>
            t("common.edit"),
          ),
          // v0.3.0: BYO Node bootstrap. Hidden for
          // states that would 409 (online / draining
          // / disabled). The hint tooltip explains
          // why the entry is absent.
          isProvisionable(row.original.state)
            ? h(
                DropdownMenuItem,
                { onSelect: () => startProvision(row.original) },
                () => t("nodes.provision"),
              )
            : null,
          // v0.8.4: rotate-panel-key. Hidden for
          // `new` (the panel cannot SSH into a
          // never-installed node — the dropdown
          // shows the entry only when the node
          // has had a previous install that left
          // an authorized_keys line for the
          // panel's operator-owned PEM to dial
          // through). The `online` / `offline` /
          // `draining` / `disabled` states are
          // all valid rotation sources.
          isRotatable(row.original.state)
            ? h(
                DropdownMenuItem,
                { onSelect: () => startRotate(row.original) },
                () => t("nodes.rotate"),
              )
            : null,
          // v0.8.7: refresh-agent-bearer. Same
          // state gate as rotate-panel-key
          // (hidden for `new`; the no-stored-
          // key HTTP path returns 409 with a
          // "rotate-panel-key first" hint for
          // rows that have a state but no
          // stored key). The action calls
          // `POST /api/v1/nodes/{id}/refresh-
          // agent-bearer`, the response carries
          // the new bearer + the SHA-256
          // fingerprint of the stored panel
          // key.
          isRefreshable(row.original.state)
            ? h(
                DropdownMenuItem,
                { onSelect: () => startRefresh(row.original) },
                () => t("nodes.refresh"),
              )
            : null,
          // v0.8.5: show-stored-key. Visible for
          // ANY state (including `new` — the
          // dialog surfaces a "no stored key
          // yet" hint for the un-installed case).
          // The endpoint is a read, not a write,
          // so there's no state-machine
          // enforcement; the operator can audit
          // their fleet's stored-key surface at
          // any time.
          h(
            DropdownMenuItem,
            { onSelect: () => startInspect(row.original) },
            () => t("nodes.inspect"),
          ),
          h(
            DropdownMenuItem,
            { onSelect: () => confirmDelete(row.original) },
            () => t("common.delete"),
          ),
        ]),
      ]),
  },
];

// Quick scope check for the current user. The Go
// side enforces this; we hide the create button
// for read-only users.
//
// v0.8.x fix: the previous form relied on
// `auth.me?.scopes` to gate the write affordances.
// That worked when `/api/v1/auth/me` returned the
// caller's scopes. v0.8.0 introduced a v0.x Me()
// regression (`lookupByID only supported for
// MemoryStore in Phase 0`) that made `/me` return
// 500 on the pg backend, which cascaded into
// `auth.me === null` and hid the create button
// from EVERY user. Fall back to "assume write when
// authenticated" so the UI is usable before the
// server-side /me fix lands; the server still
// enforces scopes on every mutating endpoint, so
// this is safe (a read-only user clicking "Add node"
// still gets a 403 from the panel).
const canWrite = computed(() => {
  if (!auth.isAuthenticated) return false;
  const scopes = auth.me?.scopes ?? [];
  if (scopes.length === 0) return true; // fallback when /me is broken
  return scopes.includes("write") || scopes.includes("admin");
});
</script>

<template>
  <section class="nodes">
    <header class="nodes__header">
      <div>
        <h1 class="nodes__title">
          {{ t("nodes.title") }}
        </h1>
        <p class="nodes__subtitle">
          {{ t("nodes.subtitle") }}
        </p>
      </div>
      <Button
        v-if="canWrite"
        @click="startCreate"
      >
        <Plus class="h-4 w-4" />
        {{ t("nodes.create") }}
      </Button>
    </header>

    <DataTable
      :columns="columns"
      :data="nodes"
      :loading="loading"
      :search-key="'nodes.search'"
      :empty-key="'nodes.empty'"
    />

    <!-- Create dialog (extracted to NodeCreateDialog in v0.9.x) -->
    <NodeCreateDialog
      v-model:open="createOpen"
      @created="onNodeCreated"
    />

    <!-- Edit dialog -->
    <!-- Edit dialog (extracted to NodeEditDialog in v0.9.x) -->
    <NodeEditDialog
      v-model:open="editOpen"
      :node="editingForEdit"
      @updated="onNodeUpdated"
    />

    <!-- Provision dialog (v0.3.0) -->
    <Dialog v-model:open="provisionOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Server class="h-4 w-4 inline-block mr-2 align-text-bottom" />
            {{ t("nodes.provisionTitle") }}
          </DialogTitle>
          <DialogDescription>
            {{
              t("nodes.provisionDescription")
            }}
          </DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="provisionForm.isSubmitting.value"
          @submit="provisionForm.handleSubmit"
        >
          <p class="nodes__provision-target">
            <strong>{{ provisioning?.name }}</strong>
            ({{ provisioning?.address }}) —
            {{ provisioning ? t(`nodes.states.${provisioning.state}`) : "" }}
          </p>
          <FormField
            name="ssh_user"
            :label="t('nodes.sshUser')"
            :hint="t('nodes.sshUserHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="root"
                @update:model-value="
                  (v: string) => provisionForm.setFieldValue('ssh_user', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="ssh_port"
            :label="t('nodes.sshPort')"
            :hint="t('nodes.sshPortHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                type="number"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="22"
                @update:model-value="
                  (v: string) =>
                    provisionForm.setFieldValue(
                      'ssh_port',
                      v === '' ? undefined : Number(v),
                    )
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <!-- v0.8.x: auth-method radio. The three-way
               picker drives the conditional rendering
               of the key / password fields below, and
               the wire payload built in the onSubmit
               handler. The "stored" option is only
               available for re-provisions (state
               'offline'); for first-time installs (state
               'new') the radio is disabled because the
               panel has no key to re-use yet. -->
          <FormField
            name="authMethod"
            :label="t('nodes.authMethod')"
            required
            :hint="t('nodes.authMethodHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <RadioGroup
                :id="id"
                :model-value="(value as string) ?? 'key'"
                :aria-label="t('nodes.authMethod')"
                :class="hasError && 'rounded-md border border-destructive p-1'"
                @update:model-value="
                  (v: string) => {
                    provisionForm.setFieldValue(
                      'authMethod',
                      v as 'key' | 'password' | 'stored',
                    );
                    provisionForm.setFieldValue('ssh_private_key', '');
                    provisionForm.setFieldValue('ssh_password', '');
                    onBlur();
                  }
                "
              >
                <RadioGroupItem value="key">
                  <KeySquare class="h-4 w-4" />
                  <span>{{ t("nodes.authMethodKey") }}</span>
                </RadioGroupItem>
                <RadioGroupItem value="password">
                  <Lock class="h-4 w-4" />
                  <span>{{ t("nodes.authMethodPassword") }}</span>
                </RadioGroupItem>
                <RadioGroupItem
                  value="stored"
                  :disabled="provisioning?.state !== 'offline'"
                  :title="
                    provisioning?.state !== 'offline'
                      ? t('nodes.authMethodStoredDisabledTitle')
                      : ''
                  "
                >
                  <KeyRound class="h-4 w-4" />
                  <span>{{ t("nodes.authMethodStored") }}</span>
                </RadioGroupItem>
              </RadioGroup>
            </template>
          </FormField>
          <FormField
            v-if="provisionForm.values.authMethod === 'key'"
            name="ssh_private_key"
            :label="t('nodes.sshPrivateKey')"
            required
            :hint="t('nodes.sshPrivateKeyHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="8"
                :class="hasError && 'border-destructive'"
                spellcheck="false"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                @update:model-value="
                  (v: string) =>
                    provisionForm.setFieldValue('ssh_private_key', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            v-if="provisionForm.values.authMethod === 'password'"
            name="ssh_password"
            :label="t('nodes.sshPassword')"
            required
            :hint="t('nodes.sshPasswordHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                type="password"
                autocomplete="off"
                :model-value="String(value ?? '')"
                :class="hasError && 'border-destructive'"
                spellcheck="false"
                @update:model-value="
                  (v: string) => provisionForm.setFieldValue('ssh_password', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="tofu_policy"
            :label="t('nodes.tofuPolicy')"
            :hint="t('nodes.tofuPolicyHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <select
                :id="id"
                :value="value"
                :class="['nodes__select', hasError && 'border-destructive']"
                @change="
                  (event: Event) => {
                    const v = (event.target as HTMLSelectElement).value;
                    provisionForm.setFieldValue(
                      'tofu_policy',
                      v === ''
                        ? undefined
                        : (v as 'reject' | 'accept-and-append'),
                    );
                    onBlur();
                  }
                "
              >
                <option value="reject">
                  {{ t("nodes.tofuReject") }}
                </option>
                <option value="accept-and-append">
                  {{ t("nodes.tofuAcceptAndAppend") }}
                </option>
              </select>
            </template>
          </FormField>
          <FormField
            name="expected_fingerprint"
            :label="t('nodes.expectedFingerprint')"
            :hint="t('nodes.expectedFingerprintHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="SHA256:abc123..."
                @update:model-value="
                  (v: string) =>
                    provisionForm.setFieldValue('expected_fingerprint', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button
                type="button"
                variant="outline"
                :disabled="provisionForm.isSubmitting.value"
                @click="provisioning = null"
              >
                {{ t("common.cancel") }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="provisionForm.isSubmitting.value"
            >
              <Server class="h-4 w-4 mr-2" />
              {{ t("nodes.provision") }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Rotate-panel-key dialog (v0.8.4) -->
    <Dialog v-model:open="rotateOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <RefreshCw class="h-4 w-4 inline-block mr-2 align-text-bottom" />
            {{ t("nodes.rotateTitle") }}
          </DialogTitle>
          <DialogDescription>
            {{
              t("nodes.rotateDescription")
            }}
          </DialogDescription>
        </DialogHeader>
        <p class="nodes__provision-target">
          <strong>{{ rotating?.name }}</strong>
          ({{ rotating?.address }}) —
          {{ rotating ? t(`nodes.states.${rotating.state}`) : "" }}
        </p>
        <Form
          v-if="!rotationResult"
          :is-submitting="rotateForm.isSubmitting.value"
          @submit="rotateForm.handleSubmit"
        >
          <FormField
            name="ssh_user"
            :label="t('nodes.sshUser')"
            :hint="t('nodes.sshUserHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="root"
                @update:model-value="
                  (v: string) => rotateForm.setFieldValue('ssh_user', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="ssh_port"
            :label="t('nodes.sshPort')"
            :hint="t('nodes.sshPortHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                type="number"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                placeholder="22"
                @update:model-value="
                  (v: string) =>
                    rotateForm.setFieldValue(
                      'ssh_port',
                      v === '' ? undefined : Number(v),
                    )
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="ssh_private_key"
            :label="t('nodes.rotateSshPrivateKey')"
            required
            :hint="t('nodes.rotateSshPrivateKeyHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Textarea
                :id="id"
                :model-value="String(value ?? '')"
                :rows="10"
                :class="hasError && 'border-destructive'"
                spellcheck="false"
                placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
                @update:model-value="
                  (v: string) => rotateForm.setFieldValue('ssh_private_key', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button
                type="button"
                variant="outline"
                :disabled="rotateForm.isSubmitting.value"
                @click="closeRotateDialog"
              >
                {{ t("common.cancel") }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="rotateForm.isSubmitting.value"
            >
              <RefreshCw class="h-4 w-4" />
              {{ t("nodes.rotateAction") }}
            </Button>
          </DialogFooter>
        </Form>
        <!-- Success card. Renders after a
             200 response. The dialog stays
             open so the operator can copy the
             fingerprint before closing. The
             form is hidden (`v-if`) so the
             submit button is not visible
             alongside the success card. -->
        <div
          v-else
          class="nodes__rotation-result"
        >
          <h3 class="nodes__rotation-result-title">
            <KeyRound class="h-4 w-4 inline-block mr-2 align-text-bottom" />
            {{ t("nodes.rotateResultTitle") }}
          </h3>
          <p class="nodes__rotation-result-help">
            {{ t("nodes.rotateResultHelp") }}
          </p>
          <FormField
            name="rotate-public-key"
            :label="t('nodes.rotatePublicKeyLine')"
          >
            <template #default="{ id }">
              <Textarea
                :id="id"
                :model-value="rotationResult.public_key_line"
                :rows="4"
                readonly
                spellcheck="false"
                @update:model-value="() => {}"
              />
            </template>
          </FormField>
          <FormField
            name="rotate-fingerprint"
            :label="t('nodes.rotateFingerprint')"
          >
            <template #default="{ id }">
              <Input
                :id="id"
                :model-value="rotationResult.fingerprint"
                readonly
                @update:model-value="() => {}"
              />
            </template>
          </FormField>
          <DialogFooter>
            <Button
              type="button"
              @click="closeRotateDialog"
            >
              {{ t("common.close") }}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>

    <!-- Inspect stored key dialog (v0.8.5) -->
    <Dialog v-model:open="inspectOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            <Eye class="h-4 w-4 inline-block mr-2 align-text-bottom" />
            {{ t("nodes.inspectTitle") }}
          </DialogTitle>
          <DialogDescription>{{ t("nodes.inspectDescription") }}</DialogDescription>
        </DialogHeader>
        <p class="nodes__provision-target">
          <strong>{{ inspecting?.name }}</strong>
          ({{ inspecting?.address }})
          — {{ inspecting ? t(`nodes.states.${inspecting.state}`) : "" }}
        </p>
        <!-- Loading state. The dialog opens
             immediately (so the user can see
             the target node); the GET fires
             on open. The spinner is the
             only content until the response
             lands. -->
        <div
          v-if="inspectionLoading"
          class="nodes__inspection-loading"
        >
          {{ t("nodes.inspectLoading") }}
        </div>
        <!-- "No stored key" state. The row
             exists but the ciphertext column
             is empty (`new` nodes, or legacy
             v0.3.0..v0.7.x nodes that have not
             been back-filled with the v0.8.3
             CLI). The dialog shows a hint about
             how to populate the column. -->
        <div
          v-else-if="inspection && !inspection.has_stored_key"
          class="nodes__inspection-empty"
        >
          <p>{{ t("nodes.inspectNoKey") }}</p>
          <p class="nodes__inspection-empty-hint">
            {{ t("nodes.inspectNoKeyHint") }}
          </p>
        </div>
        <!-- Error state. The toast was
             already shown by the handler; the
             dialog shows a brief inline
             error so the user knows the
             dialog content is in a failed
             state. -->
        <div
          v-else-if="inspectionError"
          class="nodes__inspection-error"
        >
          {{ inspectionError }}
        </div>
        <!-- Success state. The dialog surfaces
             the public-key line + fingerprint
             so the operator can copy the
             fingerprint (compare against
             `ssh-add -L` on the operator's
             local box after the re-provision's
             first contact). -->
        <div
          v-else-if="inspection && inspection.has_stored_key"
          class="nodes__rotation-result"
        >
          <h3 class="nodes__rotation-result-title">
            <KeyRound class="h-4 w-4 inline-block mr-2 align-text-bottom" />
            {{ t("nodes.inspectSurfaceTitle") }}
          </h3>
          <p class="nodes__rotation-result-help">
            {{ t("nodes.inspectSurfaceHelp") }}
          </p>
          <FormField
            name="inspect-public-key"
            :label="t('nodes.rotatePublicKeyLine')"
          >
            <template #default="{ id }">
              <Textarea
                :id="id"
                :model-value="inspection.public_key_line ?? ''"
                :rows="4"
                readonly
                spellcheck="false"
                @update:model-value="() => {}"
              />
            </template>
          </FormField>
          <FormField
            name="inspect-fingerprint"
            :label="t('nodes.rotateFingerprint')"
          >
            <template #default="{ id }">
              <Input
                :id="id"
                :model-value="inspection.fingerprint ?? ''"
                readonly
                @update:model-value="() => {}"
              />
            </template>
          </FormField>
          <FormField
            v-if="inspection.key_updated_at"
            name="inspect-key-updated-at"
            :label="t('nodes.inspectKeyUpdatedAt')"
          >
            <template #default="{ id }">
              <Input
                :id="id"
                :model-value="inspection.key_updated_at"
                readonly
                @update:model-value="() => {}"
              />
            </template>
          </FormField>
        </div>
        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            @click="closeInspectDialog"
          >
            {{ t("common.close") }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Refresh agent bearer dialog (v0.8.7) -->
    <Dialog v-model:open="refreshOpen">
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
          <strong>{{ refreshing?.name }}</strong>
          ({{ refreshing?.address }})
          — {{ refreshing ? t(`nodes.states.${refreshing.state}`) : "" }}
        </p>
        <!-- Loading state. The dialog
             opens immediately (so the
             user can see the target
             node); the POST fires on
             open. The spinner is the
             only content until the
             response lands. The shape
             mirrors the inspect dialog's
             loading surface for visual
             consistency. -->
        <div
          v-if="refreshLoading && !refreshResult && !refreshError"
          class="nodes__refresh-loading"
        >
          {{ t("nodes.refreshLoading") }}
        </div>
        <!-- Error state. The 409
             "no stored key" case carries
             a "rotate-panel-key first"
             hint from the panel; the
             operator sees the full error
             message verbatim. The 502
             cases (SSH connect / run /
             agent.env parse) carry a
             specific stage name (the
             panel's error message starts
             with the failing stage). -->
        <div
          v-else-if="refreshError"
          class="nodes__refresh-error"
        >
          {{ refreshError }}
        </div>
        <!-- Success card. Renders
             after a 200 response. The
             dialog stays open so the
             operator can copy the
             new bearer before closing.
             The new bearer is the
             AEGIS_AGENT_BEARER value
             from /etc/aegis/agent.env
             on the node; the
             fingerprint is the
             SHA-256 of the stored
             panel key (proves "the
             refresh used the key I
             expect"). -->
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
              @click="closeRefreshDialog"
            >
              {{ t("common.close") }}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="deleteConfirmOpen"
      :title="t('nodes.confirmDelete', { name: pendingDelete?.name ?? '' })"
      :variant="'destructive'"
      :confirm-label="t('common.delete')"
      @confirm="performDelete"
    />
  </section>
</template>

<style scoped>
.nodes {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.nodes__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.nodes__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.nodes__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.nodes__provision-target {
  margin: 0 0 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  font-size: 0.875rem;
}

.nodes__select {
  display: block;
  width: 100%;
  border: 1px solid hsl(var(--input));
  border-radius: 0.375rem;
  background: transparent;
  padding: 0.5rem 0.75rem;
  font-size: 0.875rem;
}

.nodes__select:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px hsl(var(--ring));
  border-color: hsl(var(--ring));
}

/* v0.8.4: rotate-panel-key success card. The
   card sits inside the rotate dialog after a
   200 response; the dialog stays open so the
   operator can copy the fingerprint. The
   layout is a small column with a title, a
   one-line help hint, and the two read-only
   fields. */
.nodes__rotation-result {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin-top: 0.5rem;
}

.nodes__rotation-result-title {
  font-size: 0.95rem;
  font-weight: 600;
  margin: 0;
}

.nodes__rotation-result-help {
  font-size: 0.85rem;
  color: hsl(var(--muted-foreground));
  margin: 0;
}

/* v0.8.5: inspect-stored-key dialog states.
   The dialog has four states: loading,
   no-stored-key, error, success. The
   loading + error states are inline
   paragraphs; the no-stored-key state is
   a paragraph + a hint paragraph; the
   success state reuses the rotation-result
   styles above. */
.nodes__inspection-loading,
.nodes__inspection-empty,
.nodes__inspection-error {
  padding: 0.75rem 1rem;
  border-radius: 0.5rem;
  font-size: 0.9rem;
  margin: 0.5rem 0;
}

.nodes__inspection-loading {
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
  text-align: center;
}

.nodes__inspection-empty {
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
}

.nodes__inspection-empty-hint {
  font-size: 0.8rem;
  margin: 0.5rem 0 0;
  opacity: 0.8;
}

.nodes__inspection-error {
  background: hsl(var(--destructive) / 0.1);
  color: hsl(var(--destructive));
  font-family: monospace;
  font-size: 0.8rem;
}
</style>
