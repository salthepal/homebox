<script setup lang="ts">
  // Item page tab: projects linked to this item, plus "New project for this item" (pre-linked).
  import { useI18n } from "vue-i18n";
  import type { EntityOut } from "~~/lib/api/types/data-contracts";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import { Button } from "@/components/ui/button";
  import { toast } from "@/components/ui/sonner";
  import MdiPlus from "~icons/mdi/plus";
  import type { XProject, XProjectInput } from "../../../../lib/api/client";
  import { useCustomApi } from "../../../../lib/api/client";
  import ProjectCard from "../../../../components/ProjectCard.vue";
  import ProjectFormDialog from "../../../../components/ProjectFormDialog.vue";

  const props = defineProps<{ item: EntityOut }>();

  const { t } = useI18n();
  const api = useCustomApi();
  const router = useRouter();

  const loadFailed = ref(false);
  const { data: projects, refresh } = await useAsyncData(
    () => `x-item-projects-${props.item.id}`,
    async () => {
      const resp = await api.projectsForEntity(props.item.id);
      loadFailed.value = resp.error;
      return resp.error ? ([] as XProject[]) : (resp.data ?? []);
    },
    { watch: [() => props.item.id] }
  );

  const showNew = ref(false);
  const saving = ref(false);

  async function create(input: XProjectInput) {
    saving.value = true;
    const created = await api.createProject(input);
    if (created.error) {
      saving.value = false;
      toast.error(t("x.projects.toast.failed"));
      return;
    }
    const linked = await api.setProjectEntities(created.data.id, [props.item.id]);
    saving.value = false;
    if (linked.error) {
      toast.error(t("x.projects.item_tab.link_failed"));
    } else {
      toast.success(t("x.projects.toast.created"));
    }
    showNew.value = false;
    await refresh();
    await router.push(`/x/projects/${created.data.id}`);
  }
</script>

<template>
  <BaseContainer class="flex flex-col gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <BaseSectionHeader> {{ t("x.projects.item_tab.title") }} </BaseSectionHeader>
      <Button data-testid="x-item-new-project" @click="showNew = true">
        <MdiPlus />
        {{ t("x.projects.item_tab.new") }}
      </Button>
    </div>

    <p v-if="loadFailed" class="text-sm text-destructive">{{ t("x.projects.load_error") }}</p>
    <p v-else-if="!(projects ?? []).length" class="text-muted-foreground" data-testid="x-item-projects-empty">
      {{ t("x.projects.item_tab.empty") }}
    </p>
    <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2" data-testid="x-item-projects">
      <ProjectCard v-for="p in projects" :key="p.id" :project="p" />
    </div>

    <ProjectFormDialog v-model:open="showNew" :saving="saving" @submit="create" />
  </BaseContainer>
</template>
