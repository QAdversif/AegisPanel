<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodeEditDialog. v0.1.0 ships the edit dialog
  for /api/v1/nodes/{id}. Extracted from
  NodesView.vue in v0.9.x to keep the view's
  create / edit / list flows in separate, focused
  files. Self-contained: owns its own form state,
  the partial-update zod schema, and the
  read-after-write hydration from `props.node`.

  The dialog is a minimal patch surface (name,
  region, address, capacityHint) — the same fields
  the v0.1.0 edit dialog shipped. The form is
  hydrated from `props.node` every time the dialog
  opens so the operator edits against a
  read-after-write view (the parent re-fetches the
  node from /api/v1/nodes/{id} in `startEdit` so
  the row they click in the table is the row they
  see in the form).
-->
<script setup lang="ts">
import { watch } from "vue";
import { useI18n } from "vue-i18n";

import { updateNode } from "@/api/services";
import { toApiError } from "@/api/client";
import type { Node } from "@/types";
import { nodeUpdateSchema, type NodeUpdateInput } from "@/schemas";
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

const props = defineProps<{
  open: boolean;
  node: Node | null;
}>();

const emit = defineEmits<{
  "update:open": [value: boolean];
  updated: [node: Node];
}>();

const { t } = useI18n();
const toast = useToastStore();

const editForm = useZodForm({
  schema: nodeUpdateSchema,
  initialValues: {
    name: "",
    region: "",
    address: "",
    capacityHint: "",
  } as NodeUpdateInput,
  onSubmit: async (values) => {
    if (!props.node) return;
    try {
      // `nodeUpdateSchema` is partial+strict; the Go
      // PATCH /api/v1/nodes/{id} handler accepts
      // unknown-field rejections at the wire layer,
      // so the schema mirrors that.
      await updateNode(props.node.id, values);
      toast.add({ title: t("nodes.updated"), variant: "success" });
      emit("updated", props.node);
    } catch (error) {
      toast.add({
        title: t("nodes.updateFailed"),
        description: toApiError(error).message,
        variant: "destructive",
      });
    }
  },
});

// Hydrate the form from `props.node` every time
// the dialog opens. The parent passes the freshest
// payload through getNode() in startEdit so we
// always edit against a read-after-write view.
watch(
  () => [props.open, props.node] as const,
  ([isOpen, node]) => {
    if (isOpen && node) {
      editForm.resetForm({
        values: {
          name: node.name,
          region: node.region,
          address: node.address,
          capacityHint: node.capacityHint ?? "",
        } as NodeUpdateInput,
      });
    }
  },
  { immediate: true },
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
        <DialogTitle>{{ t("nodes.editTitle") }}</DialogTitle>
        <DialogDescription>
          {{
            t("nodes.editDescription")
          }}
        </DialogDescription>
      </DialogHeader>
      <Form
        :is-submitting="editForm.isSubmitting.value"
        @submit="editForm.handleSubmit"
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
                (v: string) => editForm.setFieldValue('name', v)
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
                (v: string) => editForm.setFieldValue('region', v)
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
              @update:model-value="
                (v: string) => editForm.setFieldValue('address', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="capacityHint"
          :label="t('nodes.capacityHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              @update:model-value="
                (v: string) => editForm.setFieldValue('capacityHint', v)
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
            >
              {{ t("common.cancel") }}
            </Button>
          </DialogClose>
          <Button
            type="submit"
            :disabled="editForm.isSubmitting.value"
          >
            {{ t("common.save") }}
          </Button>
        </DialogFooter>
      </Form>
    </DialogContent>
  </Dialog>
</template>
