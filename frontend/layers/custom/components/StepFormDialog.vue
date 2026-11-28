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
    due_date: "",
    est_cost: "",
    actual_cost: "",
    url: "",
    entity_id: null as string | null,
    log_as_maintenance: false,
  });

  watch(
    open,
    async isOpen => {
      if (!isOpen) return;
      const s = props.step;
      form.title = s?.title ?? "";
      form.notes = s?.notes ?? "";
      form.due_date = toDateInput(s?.due_date);
      form.est_cost = s?.est_cost != null ? String(s.est_cost) : "";
      form.actual_cost = s?.actual_cost != null ? String(s.actual_cost) : "";
      form.url = s?.url ?? "";
      form.entity_id = s?.entity_id ?? null;
      form.log_as_maintenance = s?.log_as_maintenance ?? false;
      await resolve([form.entity_id]);
    },
    { immediate: true }
  );

  const valid = computed(() => form.title.trim().length > 0 && form.title.length <= 255);

  async function pickItem(item: { id: string; name: string }) {
    form.entity_id = item.id;
    form.log_as_maintenance = true;
    await resolve([item.id]);
  }

  function clearItem() {
    form.entity_id = null;
    form.log_as_maintenance = false;
  }

  function submit() {
    if (!valid.value) return;
    emit("submit", {
      title: form.title.trim(),
      notes: form.notes,
      due_date: fromDateInput(form.due_date),
      est_cost: parseMoney(form.est_cost),
      actual_cost: parseMoney(form.actual_cost),
      url: form.url.trim() || null,
      entity_id: form.entity_id,
      log_as_maintenance: form.entity_id ? form.log_as_maintenance : false,
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
            <Input id="x-s-due" v-model="form.due_date" type="date" />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="x-s-est">{{ t("x.projects.steps.form.est_cost") }}</Label>
            <Input id="x-s-est" v-model="form.est_cost" type="number" min="0" step="0.01" inputmode="decimal" />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="x-s-act">{{ t("x.projects.steps.form.actual_cost") }}</Label>
            <Input id="x-s-act" v-model="form.actual_cost" type="number" min="0" step="0.01" inputmode="decimal" />
          </div>
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="x-s-url">{{ t("x.projects.steps.form.url") }}</Label>
          <Input id="x-s-url" v-model="form.url" type="url" placeholder="https://" />
        </div>

        <div class="flex flex-col gap-1.5">
          <Label>{{ t("x.projects.steps.form.item") }}</Label>
          <div
            v-if="form.entity_id"
            class="flex items-center justify-between gap-2 rounded-md border px-3 py-1.5 text-sm"
          >
            <span class="truncate">{{ nameOf(form.entity_id) }}</span>
            <Button type="button" size="sm" variant="ghost" @click="clearItem">
              {{ t("x.projects.steps.form.no_item") }}
            </Button>
          </div>
          <ItemPicker v-else @pick="pickItem" />
        </div>

        <label v-if="form.entity_id" class="flex items-center gap-2 text-sm">
          <input v-model="form.log_as_maintenance" type="checkbox" class="size-4" />
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
