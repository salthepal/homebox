<script setup lang="ts">
  // Create or edit a project's own fields. Pass `project` to edit; omit to create.
  import { useI18n } from "vue-i18n";
  import { DialogRoot } from "reka-ui";
  import { Button } from "@/components/ui/button";
  import { DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import { Textarea } from "@/components/ui/textarea";
  import type { XProject, XProjectInput, XProjectStatus } from "../lib/api/client";
  import { PROJECT_STATUSES, fromDateInput, parseMoney, toDateInput } from "../lib/projects";

  const props = defineProps<{ project?: XProject | null; saving?: boolean }>();
  const open = defineModel<boolean>("open", { required: true });
  const emit = defineEmits<{ (e: "submit", input: XProjectInput): void }>();

  const { t } = useI18n();

  const form = reactive({
    name: "",
    description: "",
    status: "idea" as XProjectStatus,
    priority: "0",
    target_date: "",
    budget: "",
  });

  watch(
    open,
    isOpen => {
      if (!isOpen) return;
      const p = props.project;
      form.name = p?.name ?? "";
      form.description = p?.description ?? "";
      form.status = p?.status ?? "idea";
      form.priority = String(p?.priority ?? 0);
      form.target_date = toDateInput(p?.target_date);
      form.budget = p?.budget != null ? String(p.budget) : "";
    },
    { immediate: true }
  );

  const valid = computed(() => form.name.trim().length > 0 && form.name.length <= 255);

  function submit() {
    if (!valid.value) return;
    emit("submit", {
      name: form.name.trim(),
      description: form.description,
      status: form.status,
      priority: Number(form.priority),
      target_date: fromDateInput(form.target_date),
      budget: parseMoney(form.budget),
    });
  }
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ project ? t("x.projects.edit") : t("x.projects.new") }}</DialogTitle>
      </DialogHeader>

      <form class="flex flex-col gap-3" @submit.prevent="submit">
        <div class="flex flex-col gap-1.5">
          <Label for="x-p-name">{{ t("x.projects.form.name") }}</Label>
          <Input id="x-p-name" v-model="form.name" maxlength="255" autofocus required />
        </div>
        <div class="flex flex-col gap-1.5">
          <Label for="x-p-desc">{{ t("x.projects.form.description") }}</Label>
          <Textarea id="x-p-desc" v-model="form.description" rows="3" />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="flex flex-col gap-1.5">
            <Label>{{ t("x.projects.form.status") }}</Label>
            <Select v-model="form.status">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="s in PROJECT_STATUSES" :key="s" :value="s">
                  {{ t(`x.projects.status.${s}`) }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label>{{ t("x.projects.priority.label") }}</Label>
            <Select v-model="form.priority">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="p in ['0', '1', '2', '3']" :key="p" :value="p">
                  {{ t(`x.projects.priority.${p}`) }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="x-p-target">{{ t("x.projects.form.target_date") }}</Label>
            <Input id="x-p-target" v-model="form.target_date" type="date" />
          </div>
          <div class="flex flex-col gap-1.5">
            <Label for="x-p-budget">{{ t("x.projects.form.budget") }}</Label>
            <Input id="x-p-budget" v-model="form.budget" type="number" min="0" step="0.01" inputmode="decimal" />
          </div>
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" @click="open = false">{{ t("x.projects.form.cancel") }}</Button>
          <Button type="submit" :disabled="!valid || saving" data-testid="x-project-save">
            {{ project ? t("x.projects.form.save") : t("x.projects.form.create") }}
          </Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </DialogRoot>
</template>
