<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import { Badge } from "@/components/ui/badge";
  import { Button } from "@/components/ui/button";
  import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
  } from "@/components/ui/alert-dialog";
  import { Progress } from "@/components/ui/progress";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import { toast } from "@/components/ui/sonner";
  import MdiArrowDown from "~icons/mdi/arrow-down";
  import MdiArrowLeft from "~icons/mdi/arrow-left";
  import MdiArrowUp from "~icons/mdi/arrow-up";
  import MdiCheck from "~icons/mdi/check";
  import MdiClose from "~icons/mdi/close";
  import MdiDelete from "~icons/mdi/delete";
  import MdiPencil from "~icons/mdi/pencil";
  import MdiPlus from "~icons/mdi/plus";
  import type {
    XCompleteStepInput,
    XProject,
    XProjectInput,
    XProjectStatus,
    XProjectStep,
    XStepInput,
    XStepStatus,
  } from "../../../lib/api/client";
  import { useCustomApi } from "../../../lib/api/client";
  import {
    PROJECT_STATUSES,
    STEP_STATUSES,
    formatDate,
    formatMoney,
    projectCosts,
    projectProgress,
    shouldPromptDone,
  } from "../../../lib/projects";
  import { useItemNames } from "../../../composables/useItemNames";
  import ItemPicker from "../../../components/ItemPicker.vue";
  import ProjectFormDialog from "../../../components/ProjectFormDialog.vue";
  import StepCompleteDialog from "../../../components/StepCompleteDialog.vue";
  import StepFormDialog from "../../../components/StepFormDialog.vue";

  const { t } = useI18n();
  const api = useCustomApi();
  const route = useRoute();
  const router = useRouter();
  const { resolve, nameOf } = useItemNames();

  definePageMeta({
    middleware: ["auth"],
  });

  const projectId = computed(() => route.params.id as string);

  const project = ref<XProject | null>(null);
  const notFound = ref(false);
  const busy = ref(false);

  useHead({
    title: computed(() => "HomeBox | " + (project.value?.name ?? t("x.projects.title"))),
  });

  async function load() {
    const resp = await api.getProject(projectId.value);
    if (resp.error) {
      notFound.value = true;
      project.value = null;
      return;
    }
    notFound.value = false;
    project.value = resp.data;
    await resolve([...resp.data.entityIds, ...resp.data.steps.map(s => s.entityId)]);
  }
  await load();

  const progress = computed(() => (project.value ? projectProgress(project.value) : null));
  const costs = computed(() => (project.value ? projectCosts(project.value) : null));
  const promptDone = ref(true);
  const showDonePrompt = computed(() => !!project.value && promptDone.value && shouldPromptDone(project.value));

  function toInput(p: XProject, patch: Partial<XProjectInput> = {}): XProjectInput {
    return {
      name: p.name,
      description: p.description,
      status: p.status,
      priority: p.priority,
      targetDate: p.targetDate ? p.targetDate.slice(0, 10) : null,
      budget: p.budget,
      ...patch,
    };
  }

  // Runs an API call, reloads on success, toasts on failure.
  async function run(fn: () => Promise<{ error: boolean }>, successMsg?: string): Promise<boolean> {
    busy.value = true;
    try {
      const resp = await fn();
      if (resp.error) {
        toast.error(t("x.projects.toast.failed"));
        await load();
        return false;
      }
      if (successMsg) toast.success(successMsg);
      await load();
      return true;
    } finally {
      busy.value = false;
    }
  }

  // ---- project ----
  const showEdit = ref(false);
  async function saveProject(input: XProjectInput) {
    if (await run(() => api.updateProject(projectId.value, input), t("x.projects.toast.updated"))) {
      showEdit.value = false;
    }
  }

  async function setStatus(status: XProjectStatus) {
    if (!project.value || status === project.value.status) return;
    await run(() => api.updateProject(projectId.value, toInput(project.value!, { status })));
  }

  async function markDone() {
    await setStatus("done");
  }

  const showDelete = ref(false);
  async function deleteProject() {
    busy.value = true;
    const resp = await api.deleteProject(projectId.value);
    busy.value = false;
    if (resp.error) {
      toast.error(t("x.projects.toast.failed"));
      return;
    }
    toast.success(t("x.projects.toast.deleted"));
    await router.push("/x/projects");
  }

  // ---- linked items ----
  async function setEntities(ids: string[]) {
    await run(() => api.setProjectEntities(projectId.value, ids));
  }
  async function linkItem(item: { id: string; name: string }) {
    if (!project.value || project.value.entityIds.includes(item.id)) return;
    await setEntities([...project.value.entityIds, item.id]);
  }
  async function unlinkItem(id: string) {
    if (!project.value) return;
    await setEntities(project.value.entityIds.filter(e => e !== id));
  }

  // ---- steps ----
  const showStepForm = ref(false);
  const editingStep = ref<XProjectStep | null>(null);
  function openAddStep() {
    editingStep.value = null;
    showStepForm.value = true;
  }
  function openEditStep(s: XProjectStep) {
    editingStep.value = s;
    showStepForm.value = true;
  }
  async function saveStep(input: XStepInput) {
    const ok = editingStep.value
      ? await run(() =>
          api.updateStep(projectId.value, editingStep.value!.id, { ...input, status: editingStep.value!.status })
        )
      : await run(() => api.addStep(projectId.value, input));
    if (ok) showStepForm.value = false;
  }

  async function deleteStep(s: XProjectStep) {
    await run(() => api.deleteStep(projectId.value, s.id));
  }

  async function move(index: number, delta: -1 | 1) {
    if (!project.value) return;
    const ids = project.value.steps.map(s => s.id);
    const target = index + delta;
    if (target < 0 || target >= ids.length) return;
    [ids[index], ids[target]] = [ids[target]!, ids[index]!];
    await run(() => api.reorderSteps(projectId.value, ids));
  }

  function stepInput(s: XProjectStep, patch: Partial<XStepInput> = {}): XStepInput {
    return {
      title: s.title,
      notes: s.notes,
      status: s.status,
      dueDate: s.dueDate ? s.dueDate.slice(0, 10) : null,
      estCost: s.estCost,
      actualCost: s.actualCost,
      url: s.url,
      entityId: s.entityId,
      logAsMaintenance: s.logAsMaintenance,
      ...patch,
    };
  }

  const showComplete = ref(false);
  const completing = ref<XProjectStep | null>(null);
  function openComplete(s: XProjectStep) {
    completing.value = s;
    showComplete.value = true;
  }
  async function confirmComplete(input: XCompleteStepInput) {
    if (!completing.value) return;
    const id = completing.value.id;
    if (await run(() => api.completeStep(projectId.value, id, input))) {
      showComplete.value = false;
      promptDone.value = true;
    }
  }

  async function setStepStatus(s: XProjectStep, status: XStepStatus) {
    if (status === s.status) return;
    if (status === "done") {
      openComplete(s);
      return;
    }
    await run(() => api.updateStep(projectId.value, s.id, stepInput(s, { status })));
  }
</script>

<template>
  <div>
    <BaseContainer class="flex flex-col gap-5">
      <NuxtLink
        to="/x/projects"
        class="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:underline"
      >
        <MdiArrowLeft /> {{ t("x.projects.back") }}
      </NuxtLink>

      <p v-if="notFound || !project" class="text-muted-foreground" data-testid="x-project-notfound">
        {{ t("x.projects.not_found") }}
      </p>

      <template v-else>
        <!-- header -->
        <header class="flex flex-col gap-3">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="min-w-0">
              <h1 class="break-words text-2xl font-semibold" data-testid="x-project-name">{{ project.name }}</h1>
              <p v-if="project.description" class="mt-1 whitespace-pre-line text-sm text-muted-foreground">
                {{ project.description }}
              </p>
            </div>
            <div class="flex shrink-0 gap-2">
              <Button variant="outline" size="sm" @click="showEdit = true">
                <MdiPencil /> {{ t("x.projects.edit") }}
              </Button>
              <Button variant="outline" size="sm" class="text-destructive" @click="showDelete = true">
                <MdiDelete /> {{ t("x.projects.detail.delete") }}
              </Button>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-3 text-sm">
            <Select :model-value="project.status" @update:model-value="v => setStatus(v as XProjectStatus)">
              <SelectTrigger class="w-40" data-testid="x-project-status"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="s in PROJECT_STATUSES" :key="s" :value="s">
                  {{ t(`x.projects.status.${s}`) }}
                </SelectItem>
              </SelectContent>
            </Select>
            <Badge v-if="project.priority > 0" variant="outline">{{
              t(`x.projects.priority.${project.priority}`)
            }}</Badge>
            <span v-if="project.targetDate" class="text-muted-foreground">
              {{ t("x.projects.card.target", { date: formatDate(project.targetDate) }) }}
            </span>
            <span v-if="project.startedAt" class="text-muted-foreground">
              {{ t("x.projects.detail.started", { date: formatDate(project.startedAt) }) }}
            </span>
            <span v-if="project.completedAt" class="text-muted-foreground">
              {{ t("x.projects.detail.completed", { date: formatDate(project.completedAt) }) }}
            </span>
          </div>

          <div v-if="progress && costs" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div class="flex flex-col gap-1 rounded-lg border p-3">
              <Progress :model-value="progress.percent" />
              <span class="text-sm" data-testid="x-project-progress">
                {{ t("x.projects.card.progress", { done: progress.done, total: progress.total }) }}
              </span>
            </div>
            <div class="grid grid-cols-3 gap-2 rounded-lg border p-3 text-sm">
              <div>
                <div class="text-xs text-muted-foreground">{{ t("x.projects.detail.budget") }}</div>
                <div>{{ costs.budget !== null ? formatMoney(costs.budget) : "—" }}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground">{{ t("x.projects.detail.estimated") }}</div>
                <div data-testid="x-project-estimated">{{ formatMoney(costs.estimated) }}</div>
              </div>
              <div>
                <div class="text-xs text-muted-foreground">{{ t("x.projects.detail.actual") }}</div>
                <div :class="costs.overBudget ? 'font-medium text-destructive' : ''" data-testid="x-project-actual">
                  {{ formatMoney(costs.actual) }}
                </div>
                <div v-if="costs.overBudget" class="text-xs text-destructive">
                  {{ t("x.projects.detail.over_budget") }}
                </div>
              </div>
            </div>
          </div>

          <div
            v-if="showDonePrompt"
            class="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-green-600/40 bg-green-600/10 p-3"
            data-testid="x-done-prompt"
          >
            <div>
              <div class="font-medium">{{ t("x.projects.detail.all_done_title") }}</div>
              <div class="text-sm text-muted-foreground">{{ t("x.projects.detail.all_done_body") }}</div>
            </div>
            <div class="flex gap-2">
              <Button size="sm" variant="ghost" @click="promptDone = false">{{
                t("x.projects.detail.dismiss")
              }}</Button>
              <Button size="sm" :disabled="busy" data-testid="x-mark-done" @click="markDone">
                <MdiCheck /> {{ t("x.projects.detail.mark_done") }}
              </Button>
            </div>
          </div>
        </header>

        <!-- linked items -->
        <section class="flex flex-col gap-2">
          <h2 class="text-lg font-semibold">{{ t("x.projects.items.title") }}</h2>
          <p v-if="!project.entityIds.length" class="text-sm text-muted-foreground">{{ t("x.projects.items.none") }}</p>
          <div v-else class="flex flex-wrap gap-2" data-testid="x-linked-items">
            <Badge v-for="id in project.entityIds" :key="id" variant="secondary" class="gap-1 pr-1">
              <NuxtLink :to="`/item/${id}`" class="hover:underline">{{ nameOf(id) }}</NuxtLink>
              <button
                type="button"
                class="rounded-full p-0.5 hover:bg-background/60"
                :aria-label="t('x.projects.items.remove')"
                :disabled="busy"
                @click="unlinkItem(id)"
              >
                <MdiClose class="size-3" />
              </button>
            </Badge>
          </div>
          <div class="max-w-sm">
            <ItemPicker :exclude="project.entityIds" :placeholder="t('x.projects.items.add')" @pick="linkItem" />
          </div>
        </section>

        <!-- steps -->
        <section class="flex flex-col gap-2">
          <div class="flex items-center justify-between">
            <h2 class="text-lg font-semibold">{{ t("x.projects.steps.title") }}</h2>
            <Button size="sm" data-testid="x-add-step" @click="openAddStep"
              ><MdiPlus /> {{ t("x.projects.steps.add") }}</Button
            >
          </div>

          <p v-if="!project.steps.length" class="text-sm text-muted-foreground">{{ t("x.projects.steps.empty") }}</p>

          <ol v-else class="flex flex-col gap-2" data-testid="x-steps">
            <li
              v-for="(s, i) in project.steps"
              :key="s.id"
              class="flex flex-col gap-2 rounded-lg border p-3 sm:flex-row sm:items-start"
              :class="s.status === 'done' || s.status === 'skipped' ? 'bg-muted/40' : ''"
              data-testid="x-step"
            >
              <div class="flex min-w-0 flex-1 flex-col gap-1">
                <div class="flex items-start gap-2">
                  <span class="mt-0.5 w-5 shrink-0 text-xs text-muted-foreground">{{ i + 1 }}.</span>
                  <span
                    class="break-words font-medium"
                    :class="s.status === 'done' || s.status === 'skipped' ? 'text-muted-foreground line-through' : ''"
                  >
                    {{ s.title }}
                  </span>
                </div>
                <p v-if="s.notes" class="whitespace-pre-line pl-7 text-sm text-muted-foreground">{{ s.notes }}</p>
                <div class="flex flex-wrap items-center gap-x-3 gap-y-1 pl-7 text-xs text-muted-foreground">
                  <span v-if="s.dueDate">{{ t("x.projects.steps.due", { date: formatDate(s.dueDate) }) }}</span>
                  <span v-if="s.estCost !== null">{{
                    t("x.projects.steps.est", { cost: formatMoney(s.estCost) })
                  }}</span>
                  <span v-if="s.actualCost !== null">{{
                    t("x.projects.steps.actual", { cost: formatMoney(s.actualCost) })
                  }}</span>
                  <NuxtLink v-if="s.entityId" :to="`/item/${s.entityId}`" class="hover:underline">
                    {{ nameOf(s.entityId) }}
                  </NuxtLink>
                  <a v-if="s.url" :href="s.url" target="_blank" rel="noopener noreferrer" class="underline">{{
                    s.url
                  }}</a>
                  <Badge v-if="s.maintenanceEntryId" variant="outline">{{ t("x.projects.steps.logged") }}</Badge>
                </div>
              </div>

              <div class="flex flex-wrap items-center gap-1 pl-7 sm:pl-0">
                <Select :model-value="s.status" @update:model-value="v => setStepStatus(s, v as XStepStatus)">
                  <SelectTrigger class="h-8 w-32 text-xs" data-testid="x-step-status"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="st in STEP_STATUSES" :key="st" :value="st">
                      {{ t(`x.projects.steps.status.${st}`) }}
                    </SelectItem>
                  </SelectContent>
                </Select>
                <Button
                  v-if="s.status !== 'done'"
                  size="sm"
                  variant="outline"
                  :disabled="busy"
                  data-testid="x-step-complete"
                  @click="openComplete(s)"
                >
                  <MdiCheck /> {{ t("x.projects.steps.complete") }}
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  class="size-8"
                  :disabled="busy || i === 0"
                  :aria-label="t('x.projects.steps.move_up')"
                  data-testid="x-step-up"
                  @click="move(i, -1)"
                >
                  <MdiArrowUp />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  class="size-8"
                  :disabled="busy || i === project.steps.length - 1"
                  :aria-label="t('x.projects.steps.move_down')"
                  data-testid="x-step-down"
                  @click="move(i, 1)"
                >
                  <MdiArrowDown />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  class="size-8"
                  :aria-label="t('x.projects.steps.edit')"
                  @click="openEditStep(s)"
                >
                  <MdiPencil />
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  class="size-8 text-destructive"
                  :disabled="busy"
                  :aria-label="t('x.projects.steps.delete')"
                  @click="deleteStep(s)"
                >
                  <MdiDelete />
                </Button>
              </div>
            </li>
          </ol>
        </section>
      </template>
    </BaseContainer>

    <ProjectFormDialog v-model:open="showEdit" :project="project" :saving="busy" @submit="saveProject" />
    <StepFormDialog v-model:open="showStepForm" :step="editingStep" :saving="busy" @submit="saveStep" />
    <StepCompleteDialog v-model:open="showComplete" :step="completing" :saving="busy" @submit="confirmComplete" />

    <AlertDialog v-model:open="showDelete">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t("x.projects.detail.delete_title") }}</AlertDialogTitle>
          <AlertDialogDescription>{{ t("x.projects.detail.delete_body") }}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{{ t("x.projects.form.cancel") }}</AlertDialogCancel>
          <AlertDialogAction data-testid="x-delete-confirm" @click="deleteProject">
            {{ t("x.projects.detail.delete_confirm") }}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
