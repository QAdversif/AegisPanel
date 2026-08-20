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

  v0.9.x: the create + edit + provision +
  rotate + refresh + inspect dialogs were
  extracted into ./dialogs/NodeCreateDialog.vue,
  ./dialogs/NodeEditDialog.vue,
  ./dialogs/NodeProvisionDialog.vue,
  ./dialogs/NodeRotateDialog.vue,
  ./dialogs/NodeRefreshDialog.vue, and
  ./dialogs/NodeInspectDialog.vue. The view
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
import { MoreHorizontal, Plus } from "lucide-vue-next";
import type { ColumnDef } from "@tanstack/vue-table";

import { useAuthStore } from "@/stores/auth";
import { useToastStore } from "@/stores/toast";
import { toApiError } from "@/api/client";
import { deleteNode, listNodes } from "@/api/services";
import type { Node, NodeState } from "@/types";

import DataTable from "@/components/DataTable.vue";
import Button from "@/components/ui/Button.vue";
import Badge from "@/components/ui/Badge.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import DropdownMenu from "@/components/ui/DropdownMenu.vue";
import DropdownMenuTrigger from "@/components/ui/DropdownMenuTrigger.vue";
import DropdownMenuContent from "@/components/ui/DropdownMenuContent.vue";
import DropdownMenuItem from "@/components/ui/DropdownMenuItem.vue";
import NodeCreateDialog from "./dialogs/NodeCreateDialog.vue";
import NodeEditDialog from "./dialogs/NodeEditDialog.vue";
import NodeProvisionDialog from "./dialogs/NodeProvisionDialog.vue";
import NodeRotateDialog from "./dialogs/NodeRotateDialog.vue";
import NodeRefreshDialog from "./dialogs/NodeRefreshDialog.vue";
import NodeInspectDialog from "./dialogs/NodeInspectDialog.vue";

const { t } = useI18n();
const auth = useAuthStore();
const toast = useToastStore();

const nodes = ref<Node[]>([]);
const loading = ref(false);
// v0.9.x: the create + edit + provision
// dialogs each own their own form state.
// The view keeps the trigger refs
// (`createOpen` / `editOpen` /
// `provisionOpen`) and the per-row pointers
// (`editingForEdit` / `provisioning`) for
// the edit + provision dialogs. Rotate /
// Refresh / Inspect keep their own
// per-action refs (`rotating`, `refreshing`,
// `inspecting`) so they can each render a
// per-row context card without sharing a
// single slot.
const createOpen = ref(false);
const editOpen = ref(false);
const editingForEdit = ref<Node | null>(null);

// v0.3.0 + v0.9.x: provision dialog. The
// "provisioning" node is the one currently
// being installed; the dialog stays open
// until the install completes (sync call)
// and shows the new state on success. The
// view keeps the trigger refs — the dialog
// itself owns the form + wire-payload
// builder and hydrates from `props.node`
// every time it opens.
const provisioning = ref<Node | null>(null);
const provisionOpen = ref(false);

// v0.8.4 + v0.9.x: rotate-panel-key dialog. The
// "rotating" node is the one whose panel key is
// being regenerated. The dialog (extracted to
// NodeRotateDialog in v0.9.x) owns the form, the
// success card, and the wire-payload builder. The
// view keeps the trigger ref + the per-row
// pointer (mirroring the provision / edit
// dialogs' pattern).
const rotating = ref<Node | null>(null);
const rotateOpen = ref(false);

// v0.8.5 + v0.8.7 + v0.9.x: inspect + refresh
// dialogs. The "inspecting" / "refreshing"
// nodes are the ones the operator wants to
// audit / rotate the bearer for. The dialogs
// (extracted to NodeInspectDialog +
// NodeRefreshDialog in v0.9.x) own the
// loading/empty/error/result state and the
// wire calls. The view keeps the trigger refs
// + the per-row pointers (matching the
// rotate / provision / edit pattern).
const inspecting = ref<Node | null>(null);
const inspectOpen = ref(false);
const refreshing = ref<Node | null>(null);
const refreshOpen = ref(false);

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

// v0.9.x: the provision dialog owns its own
// form state and the wire-payload builder.
// The view's job is to (a) set the per-row
// `provisioning` pointer and (b) flip the
// `provisionOpen` trigger; the dialog's
// watcher hydrates the form from `props.node`
// every time it opens (state 'offline' ->
// default 'stored' auth, anything else ->
// default 'key'). The dialog emits
// `provisioned` (with the row + the new
// state) on success and `update:open` on
// close; the view handles the toast + the
// list refresh.
function startProvision(node: Node): void {
  provisioning.value = node;
  provisionOpen.value = true;
}

function onProvisioned(_node: Node, newState: string): void {
  // The dialog already emitted `update:open`
  // -> false, so the v-model flipped
  // `provisionOpen` back. We just need to
  // surface the state transition to the
  // operator and re-fetch the list so the
  // new row state shows up in the table.
  toast.add({
    title: t("nodes.provisioned", { state: newState }),
    variant: newState === "online" ? "success" : "destructive",
  });
  void refresh();
}

// --- Rotate (v0.8.4) -------------------------------------------------------

// v0.9.x: the rotate dialog owns its own form
// state, the wire-payload builder, and the
// post-submit success card (public key line +
// SHA256 fingerprint). The view's job is to
// (a) set the per-row `rotating` pointer and
// (b) flip the `rotateOpen` trigger; the
// dialog's watcher hydrates the form + clears
// any stale `rotationResult` every time it
// opens. The dialog emits `rotated` (with the
// row) on success and `update:open` on close;
// the view handles the toast (and decides
// that no list refresh is needed).
function startRotate(node: Node): void {
  rotating.value = node;
  rotateOpen.value = true;
}

function onRotated(node: Node): void {
  // The dialog's success card stays open
  // (the `update:open` -> false fires only
  // when the operator clicks Close on the
  // success card or hits Cancel / ESC). The
  // list does not need a refresh — the
  // row's state machine did not change; only
  // the row's encrypted
  // ssh_private_key_ciphertext column changed,
  // and the panel does not expose that field
  // in the wire shape (Node doesn't include
  // it).
  toast.add({
    title: t("nodes.rotated", { name: node.name }),
    variant: "success",
  });
}

// --- Refresh agent bearer (v0.8.7, extracted to NodeRefreshDialog in v0.9.x) -

// v0.9.x: the refresh dialog owns its own
// loading/result/error state and the
// fire-and-forget wire call. The view's
// job is to (a) set the per-row `refreshing`
// pointer and (b) flip the `refreshOpen`
// trigger; the dialog's watcher hydrates
// the loading state and fires the POST
// every time it opens. The dialog emits
// `refreshed` (with the row) on success
// and `failed` (with the row + error) on
// failure; the view handles the toasts
// (and decides that no list refresh is
// needed).
function startRefresh(node: Node): void {
  refreshing.value = node;
  refreshOpen.value = true;
}

function onRefreshed(node: Node): void {
  // The dialog's success card stays open
  // (the `update:open` -> false fires only
  // when the operator clicks Close on the
  // success card or hits Cancel / ESC).
  // The list does not need a refresh —
  // the row's state machine did not
  // change; only the agent bearer
  // (nodes.agent_bearer) changed, and
  // the panel does not expose that
  // field in the wire shape (Node
  // doesn't include it).
  toast.add({
    title: t("nodes.refreshed", { name: node.name }),
    variant: "success",
  });
}

function onRefreshFailed(_node: Node, error: string): void {
  toast.add({
    title: t("nodes.refreshFailed"),
    description: error,
    variant: "destructive",
  });
}

// --- Inspect stored key (v0.8.5, extracted to NodeInspectDialog in v0.9.x) ---

// v0.9.x: the inspect dialog owns its own
// loading/empty/error/result state and the
// fire-and-forget wire call. The view's
// job is to (a) set the per-row `inspecting`
// pointer and (b) flip the `inspectOpen`
// trigger; the dialog's watcher hydrates
// the loading state and fires the GET
// every time it opens. The dialog emits
// `failed` (with the row + error) on
// failure; the view handles the toast
// (no `inspected` emit — the success
// card is the only post-inspect surface
// and the list is not refreshed because
// the inspect endpoint is a read, not a
// write).
function startInspect(node: Node): void {
  inspecting.value = node;
  inspectOpen.value = true;
}

function onInspectFailed(_node: Node, error: string): void {
  toast.add({
    title: t("nodes.inspectFailed"),
    description: error,
    variant: "destructive",
  });
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

    <!-- Provision dialog (v0.3.0, extracted to NodeProvisionDialog in v0.9.x) -->
    <NodeProvisionDialog
      v-model:open="provisionOpen"
      :node="provisioning"
      @provisioned="onProvisioned"
    />

    <!-- Rotate-panel-key dialog (v0.8.4, extracted to NodeRotateDialog in v0.9.x) -->
    <NodeRotateDialog
      v-model:open="rotateOpen"
      :node="rotating"
      @rotated="onRotated"
    />

    <!-- Inspect stored key dialog (v0.8.5, extracted to NodeInspectDialog in v0.9.x) -->
    <NodeInspectDialog
      v-model:open="inspectOpen"
      :node="inspecting"
      @failed="onInspectFailed"
    />

    <!-- Refresh agent bearer dialog (v0.8.7, extracted to NodeRefreshDialog in v0.9.x) -->
    <NodeRefreshDialog
      v-model:open="refreshOpen"
      :node="refreshing"
      @refreshed="onRefreshed"
      @failed="onRefreshFailed"
    />

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

/* v0.9.x: the inspect-stored-key dialog
   states (loading / empty / error / success)
   moved into NodeInspectDialog (extracted
   from this view). The `.nodes__refresh-*`
   style block lived here conceptually in
   v0.8.7 but was never defined (pre-existing
   oversight); the new rules live in
   NodeRefreshDialog and are not duplicated
   in this view. The `.nodes__provision-target`
   + `.nodes__rotation-result*` rules below
   are SHARED with the dialogs (each dialog
   duplicates them for self-containment); a
   follow-up PR may drop the duplicates here. */
</style>
