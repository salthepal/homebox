<script setup lang="ts">
  // Add or edit a step. Pass `step` to edit; omit to add.
  import { useI18n } from "vue-i18n";
  import { DialogRoot } from "reka-ui";
  import { Button } from "@/components/ui/button";
  import { DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { Textarea } from "@/components/ui/textarea";
  import type { XProjectStep, XStepInput } from "../lib/api/client";
  import { fromDateInput, parseMoney, toDateInput } from "../lib/projects";
  import { useItemNames } from "../composables/useItemNames";
  import ItemPicker from "./ItemPicker.vue";

  const props = defineProps<{ step?: XProjectStep | null; saving?: boolean }>();
  const open = defineModel<boolean>("open", { required: true });
  const emit = defineEmits<{ (e: "submit", input: XStepInput): void }>();

  const { t } = useI18n();
  const { resolve, nameOf } = useItemNames();

  const form = reactive({
    title: "",
    notes: "",
    dueDate: "",
    estCost: "",
    actualCost: "",
    url: "",
    entityId: null as string | null,
    logAsMaintenance: false,
  });

  watch(
    open,
    async isOpen => {
      if (!isOpen) return;
      const s = props.step;
      form.title = s?.title ?? "";
      form.notes = s?.notes ?? "";
      form.dueDate = toDateInput(s?.dueDate);
      form.estCost = s?.estCost != null ? String(s.estCost) : "";
      form.actualCost = s?.actualCost != null ? String(s.actualCost) : "";
      form.url = s?.url ?? "";
      form.entityId = s?.entityId ?? null;
      form.logAsMaintenance = s?.logAsMaintenance ?? false;
      await resolve([form.entityId]);
    },
    { immediate: true }
  );

  const valid = computed(() => form.title.trim().length > 0 && form.title.length <= 255);

  async function pickItem(item: { id: string; name: string }) {
    form.entityId = item.id;
    form.logAsMaintenance = true;
    await resolve([item.id]);
  }

  function clearItem() {
    form.entityId = null;
    form.logAsMaintenance = false;
  }

  function submit() {
    if (!valid.value) return;
    emit("submit", {
      title: form.title.trim(),
      notes: form.notes,
      dueDate: fromDateInput(form.dueDate),
      estCost: parseMoney(form.estCost),
      actualCost: parseMoney(form.actualCost),
      url: form.url.trim() || null,
      entityId: form.entityId,
      logAsMaintenance: form.entityId ? form.logAsMaintenance : false,
    });
  }
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ step ? t("x.projects.steps.edit") : t("x.projects.steps.add") }}</DialogTitle>
      </DialogHeader>

      <form class="flex flex-col gap-3" @submit.prevent="submit">
        <div class="flex flex-col gap-1.5">
          <Label for="x-s-title">{{ t("x.projects.steps.form.title") }}</Label>
          <Input id="x-s-title" v-model="form.title" maxlength="255" autofocus required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="x-s-notes">{{ t("x.projects.steps.form.notes") }}</Label>
          <Textarea id="x-s-notes" v-model="form.notes" rows="2" />
        </div>
        <div class="grid grid-cols-3 gap-3">
          <div class="flex flex-col gap-1.5">
            <Label for="x-s-due">{{ t("x.projects.steps.form.due_date") }}</Label>
            <Input id="x-s-due" v-model="form.dueDate" type="date" />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="x-s-est">{{ t("x.projects.steps.form.est_cost") }}</Label>
            <Input id="x-s-est" v-model="form.estCost" type="number" min="0" step="0.01" inputmode="decimal" />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="x-s-act">{{ t("x.projects.steps.form.actual_cost") }}</Label>
            <Input id="x-s-act" v-model="form.actualCost" type="number" min="0" step="0.01" inputmode="decimal" />
          </div>
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="x-s-url">{{ t("x.projects.steps.form.url") }}</Label>
          <Input id="x-s-url" v-model="form.url" type="url" placeholder="https://" />
        </div>

        <div class="flex flex-col gap-1.5">
          <Label>{{ t("x.projects.steps.form.item") }}</Label>
          <div
            v-if="form.entityId"
            class="flex items-center justify-between gap-2 rounded-md border px-3 py-1.5 text-sm"
          >
            <span class="truncate">{{ nameOf(form.entityId) }}</span>
            <Button type="button" size="sm" variant="ghost" @click="clearItem">
              {{ t("x.projects.steps.form.no_item") }}
            </Button>
          </div>
          <ItemPicker v-else @pick="pickItem" />
        </div>

        <label v-if="form.entityId" class="flex items-center gap-2 text-sm">
          <input v-model="form.logAsMaintenance" type="checkbox" class="size-4" />
          {{ t("x.projects.steps.form.log_as_maintenance") }}
        </label>

        <DialogFooter>
          <Button type="button" variant="ghost" @click="open = false">{{ t("x.projects.form.cancel") }}</Button>
          <Button type="submit" :disabled="!valid || saving" data-testid="x-step-save">
            {{ step ? t("x.projects.steps.form.save") : t("x.projects.steps.form.add") }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </DialogRoot>
</template>
