<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodeCreateDialog. v0.1.0 ships the create dialog
  for /api/v1/nodes. Extracted from NodesView.vue in
  v0.9.x to keep the view's create / edit / list
  flows in separate, focused files. Self-contained:
  owns its own form state, the merged
  "add + provision" zod schema, and the two-step
  create + optional provision submit.

  v0.8.12: the create form uses `nodeAddSchema`
  (the merged schema with an optional provision
  section). When the operator checks "Provision
  this node after registering" (default on), the
  submit handler calls `createNode` then
  `provisionNode` in sequence. When unchecked,
  only `createNode` is called (the v0.8.11
  behaviour). The per-row "Provision" dropdown
  entry still exists for re-provisioning offline
  nodes.

  The "Provision after create" toggle is mirrored
  to `createForm.values.provisionNow` so the Zod
  validation rules in `nodeAddSchema` see the
  same value as the template's `v-if`.
-->
<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { KeySquare, Lock } from "lucide-vue-next";

import { createNode, provisionNode } from "@/api/services";
import { toApiError } from "@/api/client";
import type { Node } from "@/types";
import { nodeAddSchema, type NodeAddInput, type NodeCreateInput } from "@/schemas";
import { useZodForm } from "@/composables/useZodForm";
import { useToastStore } from "@/stores/toast";

import Button from "@/components/ui/Button.vue";
import Dialog from "@/components/ui/Dialog.vue";
import DialogContent from "@/components/ui/DialogContent.vue";
import DialogHeader from "@/components/ui/DialogHeader.vue";
import DialogTitle from "@/components/ui/DialogTitle.vue";
import DialogDescription from "@/components/ui/DialogDescription.vue";
import DialogFooter from "@/components/ui/DialogFooter.vue";
import DialogClose from "@/components/ui/DialogClose.vue";
import Form from "@/components/Form.vue";
import FormField from "@/components/FormField.vue";
import Input from "@/components/ui/Input.vue";
import Textarea from "@/components/ui/Textarea.vue";
import RadioGroup from "@/components/ui/RadioGroup.vue";
import RadioGroupItem from "@/components/ui/RadioGroupItem.vue";

const props = defineProps<{
  open: boolean;
}>();

const emit = defineEmits<{
  "update:open": [value: boolean];
  created: [node: Node];
}>();

const { t } = useI18n();
const toast = useToastStore();

// v0.8.12: the "Provision this node after
// registering" checkbox on the Add dialog
// drives whether the submit handler calls
// just `createNode` (one round-trip) or
// `createNode` followed by `provisionNode`
// (two round-trips). Default: true. The
// checkbox is rendered above the auth-method
// radio; toggling it on/off shows/hides the
// provision field section. Mirrored to
// `createForm.values.provisionNow` so the Zod
// validation rules in `nodeAddSchema` see
// the same value as the template's `v-if`.
const provisionAfterCreate = ref(true);
function setProvisionAfterCreate(on: boolean): void {
  provisionAfterCreate.value = on;
  createForm.setFieldValue("provisionNow", on);
}

const createForm = useZodForm({
  schema: nodeAddSchema,
  initialValues: {
    name: "",
    region: "",
    capacityHint: "",
    address: "",
    provisionNow: true,
    authMethod: "key" as const,
    ssh_user: "",
    ssh_private_key: "",
    ssh_password: "",
    ssh_port: undefined,
    tofu_policy: "reject" as const,
    expected_fingerprint: "",
  } as NodeAddInput,
  onSubmit: async (values) => {
    try {
      // Always do the create first.
      const createPayload: NodeCreateInput = {
        name: values.name,
        region: values.region,
        capacityHint: values.capacityHint || undefined,
        address: values.address,
        tags: values.tags,
      };
      const created = await createNode(createPayload);
      // v0.8.12: when `provisionNow` is on, the
      // second round-trip. The auth-method radio
      // values map to the wire payload the same
      // way the existing `provisionForm` does
      // (see `startProvision`'s onSubmit below).
      if (!values.provisionNow) {
        toast.add({ title: t("nodes.created"), variant: "success" });
        emit("created", created);
      } else {
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
        try {
          const res = await provisionNode(
            created.id,
            wirePayload as Parameters<typeof provisionNode>[1],
          );
          toast.add({
            title: t("nodes.createdAndProvisioned", {
              state: res.new_state,
            }),
            variant: res.new_state === "online" ? "success" : "destructive",
          });
          emit("created", created);
        } catch (provisionError) {
          // v0.8.12: partial success. The node
          // IS registered (the first round-trip
          // succeeded); only the second failed.
          // Surface a non-fatal toast that names
          // the failure and tells the operator
          // how to retry. The form closes either
          // way (the operator can re-provision
          // from the row's Provision entry).
          toast.add({
            title: t("nodes.createdProvisionFailed"),
            description: toApiError(provisionError).message,
            variant: "destructive",
          });
          // v0.8.12: still close on partial
          // success (the node IS registered;
          // the operator can re-provision from
          // the row's Provision entry).
          emit("created", created);
        }
      }
    } catch (error) {
      toast.add({
        title: t("nodes.createFailed"),
        description: toApiError(error).message,
        variant: "destructive",
      });
    }
  },
});

// Reset the form every time the dialog opens so
// stale state from a previous open never leaks
// into a new node record.
watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      createForm.resetForm({
        values: {
          name: "",
          region: "",
          capacityHint: "",
          address: "",
          provisionNow: true,
          authMethod: "key" as const,
          ssh_user: "",
          ssh_private_key: "",
          ssh_password: "",
          ssh_port: undefined,
          tofu_policy: "reject" as const,
          expected_fingerprint: "",
        } as NodeAddInput,
      });
      provisionAfterCreate.value = true;
    }
  },
);

function onOpenChange(value: boolean): void {
  emit("update:open", value);
}
</script>

<template>
  <Dialog
    :open="open"
    @update:open="onOpenChange"
  >
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ t("nodes.createTitle") }}</DialogTitle>
        <DialogDescription>
          {{
            t("nodes.createDescription")
          }}
        </DialogDescription>
      </DialogHeader>
      <Form
        :is-submitting="createForm.isSubmitting.value"
        @submit="createForm.handleSubmit"
      >
        <FormField
          name="name"
          :label="t('nodes.name')"
          required
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              @update:model-value="
                (v: string) => createForm.setFieldValue('name', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="region"
          :label="t('nodes.region')"
          required
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              @update:model-value="
                (v: string) => createForm.setFieldValue('region', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="address"
          :label="t('nodes.address')"
          required
          :hint="t('nodes.addressHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              placeholder="node1.example.com:22"
              @update:model-value="
                (v: string) => createForm.setFieldValue('address', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="capacityHint"
          :label="t('nodes.capacityHint')"
          :hint="t('nodes.capacityHintHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              placeholder="1 Gbps"
              @update:model-value="
                (v: string) => createForm.setFieldValue('capacityHint', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <!-- v0.8.12: "Provision after create" toggle.
             When on (the default), the form reveals
             the auth-method radio + key / password /
             ssh_user / ssh_port / tofu_policy /
             fingerprint fields below. When off, the
             submit handler only calls createNode
             (the v0.8.11 behaviour). The "stored"
             option is omitted here (a brand-new
             node has no panel-stored key yet); the
             re-provision dialog on existing rows
             keeps the three-way radio. -->
        <div class="nodes__provision-toggle">
          <label class="nodes__provision-toggle-label">
            <input
              type="checkbox"
              :checked="provisionAfterCreate"
              @change="
                (event: Event) =>
                  setProvisionAfterCreate((event.target as HTMLInputElement).checked)
              "
            >
            <span class="ml-2 font-medium">
              {{ t("nodes.provisionAfterCreate") }}
            </span>
          </label>
          <p class="nodes__provision-toggle-hint">
            {{ t("nodes.provisionAfterCreateHint") }}
          </p>
        </div>
        <template v-if="provisionAfterCreate">
          <FormField
            name="ssh_user"
            :label="t('nodes.sshUser')"
            :hint="t('nodes.sshUserHint')"
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="String(value ?? '')"
                :class="hasError && 'border-destructive'"
                placeholder="root"
                @update:model-value="
                  (v: string) => createForm.setFieldValue('ssh_user', v)
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
                :model-value="value === undefined || value === null ? '' : String(value)"
                :class="hasError && 'border-destructive'"
                placeholder="22"
                @update:model-value="
                  (v: string) =>
                    createForm.setFieldValue(
                      'ssh_port',
                      v === '' ? undefined : Number(v),
                    )
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
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
                    createForm.setFieldValue(
                      'authMethod',
                      v as 'key' | 'password' | 'stored',
                    );
                    createForm.setFieldValue('ssh_private_key', '');
                    createForm.setFieldValue('ssh_password', '');
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
              </RadioGroup>
            </template>
          </FormField>
          <FormField
            v-if="createForm.values.authMethod === 'key'"
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
                    createForm.setFieldValue('ssh_private_key', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            v-if="createForm.values.authMethod === 'password'"
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
                  (v: string) => createForm.setFieldValue('ssh_password', v)
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
                    createForm.setFieldValue(
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
                :model-value="String(value ?? '')"
                :class="hasError && 'border-destructive'"
                placeholder="SHA256:abc123..."
                @update:model-value="
                  (v: string) =>
                    createForm.setFieldValue('expected_fingerprint', v)
                "
                @blur="onBlur"
              />
            </template>
          </FormField>
        </template>
        <DialogFooter>
          <DialogClose>
            <Button
              type="button"
              variant="outline"
            >
              {{ t("common.cancel") }}
            </Button>
          </DialogClose>
          <Button
            type="submit"
            :disabled="createForm.isSubmitting.value"
          >
            {{
              provisionAfterCreate
                ? t("nodes.registerAndProvision")
                : t("nodes.registerOnly")
            }}
          </Button>
        </DialogFooter>
      </Form>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.nodes__provision-toggle {
  margin: 0.5rem 0 0.25rem;
}

.nodes__provision-toggle-label {
  display: inline-flex;
  align-items: center;
  cursor: pointer;
}

.nodes__provision-toggle-hint {
  font-size: 0.8rem;
  color: hsl(var(--muted-foreground));
  margin: 0.25rem 0 0;
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
</style>
