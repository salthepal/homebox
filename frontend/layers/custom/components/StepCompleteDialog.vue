<script setup lang="ts">
  // "Complete step" dialog: actual cost + "log as maintenance" toggle (default on when the step has an item).
  import { useI18n } from "vue-i18n";
  import { DialogRoot } from "reka-ui";
  import { Button } from "@/components/ui/button";
  import { DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import type { XCompleteStepInput, XProjectStep } from "../lib/api/client";
  import { parseMoney } from "../lib/projects";
  import { useItemNames } from "../composables/useItemNames";

  const props = defineProps<{ step: XProjectStep | null; saving?: boolean }>();
  const open = defineModel<boolean>("open", { required: true });
  const emit = defineEmits<{ (e: "submit", input: XCompleteStepInput): void }>();

  const { t } = useI18n();
  const { resolve, nameOf } = useItemNames();

  const actualCost = ref("");
  const logAsMaintenance = ref(false);
  const hasItem = computed(() => !!props.step?.entityId);

  watch(
    open,
    async isOpen => {
      if (!isOpen || !props.step) return;
      actualCost.value =
        props.step.actualCost != null
          ? String(props.step.actualCost)
          : props.step.estCost != null
            ? String(props.step.estCost)
            : "";
      logAsMaintenance.value = !!props.step.entityId;
      await resolve([props.step.entityId]);
    },
    { immediate: true }
  );

  function submit() {
    emit("submit", {
      actualCost: parseMoney(actualCost.value),
      logAsMaintenance: hasItem.value && logAsMaintenance.value,
    });
  }
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ t("x.projects.steps.complete_dialog.title") }}</DialogTitle>
      </DialogHeader>

      <form class="flex flex-col gap-3" @submit.prevent="submit">
        <p class="text-sm font-medium">{{ step?.title }}</p>

        <div class="flex flex-col gap-1.5">
          <Label for="x-c-cost">{{ t("x.projects.steps.complete_dialog.actual_cost") }}</Label>
          <Input id="x-c-cost" v-model="actualCost" type="number" min="0" step="0.01" inputmode="decimal" autofocus />
        </div>

        <div class="flex flex-col gap-1">
          <label class="flex items-center gap-2 text-sm" :class="hasItem ? '' : 'opacity-60'">
            <input
              v-model="logAsMaintenance"
              type="checkbox"
              class="size-4"
              :disabled="!hasItem"
              data-testid="x-complete-log"
            />
            {{ t("x.projects.steps.complete_dialog.log_as_maintenance") }}
          </label>
          <span class="text-xs text-muted-foreground">
            {{
              hasItem
                ? t("x.projects.steps.complete_dialog.log_hint", { item: nameOf(step?.entityId) })
                : t("x.projects.steps.complete_dialog.log_needs_item")
            }}
          </span>
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" @click="open = false">{{ t("x.projects.form.cancel") }}</Button>
          <Button type="submit" :disabled="saving" data-testid="x-complete-confirm">
            {{ t("x.projects.steps.complete_dialog.confirm") }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </DialogRoot>
</template>
