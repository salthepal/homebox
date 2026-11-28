<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { Badge } from "@/components/ui/badge";
  import { Progress } from "@/components/ui/progress";
  import type { XProject } from "../lib/api/client";
  import { formatDate, formatMoney, projectCosts, projectProgress } from "../lib/projects";
  import { useItemNames } from "../composables/useItemNames";

  const props = defineProps<{ project: XProject }>();
  const { t } = useI18n();
  const { resolve, nameOf } = useItemNames();

  const progress = computed(() => projectProgress(props.project));
  const costs = computed(() => projectCosts(props.project));

  watch(
    () => props.project.entityIds,
    ids => resolve(ids),
    { immediate: true }
  );
</script>

<template>
  <NuxtLink
    :to="`/x/projects/${project.id}`"
    class="flex flex-col gap-2 rounded-lg border bg-card p-3 text-card-foreground shadow-sm transition-colors hover:bg-accent/40"
    data-testid="x-project-card"
  >
    <div class="flex items-start justify-between gap-2">
      <span class="font-medium leading-tight">{{ project.name }}</span>
      <Badge v-if="project.priority > 0" variant="outline" class="shrink-0">
        {{ t(`x.projects.priority.${project.priority}`) }}
      </Badge>
    </div>

    <div class="flex flex-col gap-1">
      <Progress :model-value="progress.percent" />
      <span class="text-xs text-muted-foreground">
        {{
          progress.total === 0
            ? t("x.projects.card.no_steps")
            : t("x.projects.card.progress", { done: progress.done, total: progress.total })
        }}
      </span>
    </div>

    <div class="flex flex-wrap items-center justify-between gap-x-3 text-xs text-muted-foreground">
      <span :class="costs.overBudget ? 'font-medium text-destructive' : ''">
        {{
          costs.budget !== null
            ? t("x.projects.card.cost", { actual: formatMoney(costs.actual), budget: formatMoney(costs.budget) })
            : t("x.projects.card.cost_no_budget", { actual: formatMoney(costs.actual) })
        }}
      </span>
      <span v-if="project.targetDate">{{ t("x.projects.card.target", { date: formatDate(project.targetDate) }) }}</span>
    </div>

    <div v-if="project.entityIds.length" class="flex flex-wrap gap-1">
      <Badge v-for="id in project.entityIds" :key="id" variant="secondary" class="max-w-full truncate">
        {{ nameOf(id) }}
      </Badge>
    </div>
  </NuxtLink>
</template>
