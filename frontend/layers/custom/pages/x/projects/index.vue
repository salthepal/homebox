<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import { Button } from "@/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import MdiPlus from "~icons/mdi/plus";
  import type { XProject, XProjectInput } from "../../../lib/api/client";
  import { useCustomApi } from "../../../lib/api/client";
  import { PROJECT_STATUSES, groupByStatus } from "../../../lib/projects";
  import ProjectCard from "../../../components/ProjectCard.vue";
  import ProjectFormDialog from "../../../components/ProjectFormDialog.vue";

  const { t } = useI18n();
  const api = useCustomApi();
  const router = useRouter();

  definePageMeta({
    middleware: ["auth"],
  });
  useHead({
    title: "HomeBox | " + t("x.projects.title"),
  });

  const loadFailed = ref(false);
  const { data: projects, refresh } = await useAsyncData("x-projects", async () => {
    const resp = await api.listProjects();
    loadFailed.value = resp.error;
    return resp.error ? ([] as XProject[]) : (resp.data ?? []);
  });

  const grouped = computed(() => groupByStatus(projects.value ?? []));
  // Terminal statuses are hidden while empty, so the board stays compact.
  const visibleStatuses = computed(() =>
    PROJECT_STATUSES.filter(s => grouped.value[s].length > 0 || !["done", "cancelled"].includes(s))
  );

  const showNew = ref(false);
  const saving = ref(false);

  async function create(input: XProjectInput) {
    saving.value = true;
    const resp = await api.createProject(input);
    saving.value = false;
    if (resp.error) {
      toast.error(t("x.projects.toast.failed"));
      return;
    }
    toast.success(t("x.projects.toast.created"));
    showNew.value = false;
    await refresh();
    await router.push(`/x/projects/${resp.data.id}`);
  }
</script>

<template>
  <div>
    <BaseContainer class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <BaseSectionHeader> {{ t("x.projects.title") }} </BaseSectionHeader>
          <p class="text-sm text-muted-foreground">{{ t("x.projects.subtitle") }}</p>
        </div>
        <Button data-testid="x-new-project" @click="showNew = true">
          <MdiPlus />
          {{ t("x.projects.new") }}
        </Button>
      </div>

      <p v-if="loadFailed" class="text-sm text-destructive">{{ t("x.projects.load_error") }}</p>
      <p v-else-if="!(projects ?? []).length" class="text-muted-foreground" data-testid="x-projects-empty">
        {{ t("x.projects.empty") }}
      </p>

      <div v-else class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" data-testid="x-projects-board">
        <section v-for="status in visibleStatuses" :key="status" class="flex flex-col gap-2 rounded-lg bg-muted/40 p-3">
          <h2 class="flex items-center justify-between text-sm font-semibold">
            {{ t(`x.projects.status.${status}`) }}
            <span class="text-xs font-normal text-muted-foreground">{{ grouped[status].length }}</span>
          </h2>
          <ProjectCard v-for="p in grouped[status]" :key="p.id" :project="p" />
          <p v-if="!grouped[status].length" class="text-xs text-muted-foreground">
            {{ t("x.projects.none_in_status") }}
          </p>
        </section>
      </div>
    </BaseContainer>

    <ProjectFormDialog v-model:open="showNew" :saving="saving" @submit="create" />
  </div>
</template>
