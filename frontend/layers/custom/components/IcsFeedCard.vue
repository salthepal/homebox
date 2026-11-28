<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import MdiLoading from "~icons/mdi/loading";
  import { Button } from "@/components/ui/button";
  import { Input } from "@/components/ui/input";
  import {
    AlertDialog,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
  } from "@/components/ui/alert-dialog";
  import { toast } from "@/components/ui/sonner";
  import { useCustomApi } from "../lib/api/client";
  import type { XIcsStatus } from "../lib/api/client";

  // Read-only ICS feed (.fork/PLAN.md G4). The token is only ever shown right after it is
  // created or rotated, so the URL lives in component state and is gone on reload.
  const { t } = useI18n();
  const api = useCustomApi();

  const status = ref<XIcsStatus | null>(null);
  const loadFailed = ref(false);
  const busy = ref(false);
  const url = ref("");
  const revokeOpen = ref(false);
  const rotateOpen = ref(false);

  function when(s: string | null) {
    return s ? new Date(s).toLocaleString() : "";
  }

  async function load() {
    const resp = await api.icsStatus();
    if (resp.error) {
      loadFailed.value = true;
      return;
    }
    loadFailed.value = false;
    status.value = resp.data;
  }

  async function issue(rotating: boolean) {
    busy.value = true;
    const resp = await api.icsIssue();
    busy.value = false;
    rotateOpen.value = false;
    if (resp.error || !resp.data.url) {
      toast.error(resp.data?.error ?? t("x.calendar.feed_failed"));
      return;
    }
    // The server may return a root-relative path when no public base URL is configured.
    url.value = new URL(resp.data.url, window.location.origin).toString();
    status.value = { exists: true, created_at: resp.data.created_at, rotated_at: resp.data.rotated_at };
    toast.success(t(rotating ? "x.calendar.feed_rotated" : "x.calendar.feed_created"));
  }

  async function revoke() {
    busy.value = true;
    const resp = await api.icsRevoke();
    busy.value = false;
    revokeOpen.value = false;
    if (resp.error) {
      toast.error(t("x.calendar.feed_failed"));
      return;
    }
    url.value = "";
    status.value = { exists: false, created_at: null, rotated_at: null };
    toast.success(t("x.calendar.feed_revoked"));
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(url.value);
      toast.success(t("x.calendar.feed_copied"));
    } catch {
      toast.error(t("x.calendar.feed_copy_failed"));
    }
  }

  onMounted(load);
</script>

<template>
  <div class="flex flex-col gap-3 rounded-md border p-4" data-testid="x-ics-card">
    <p class="font-medium">{{ t("x.calendar.feed_title") }}</p>
    <p class="text-sm text-muted-foreground">{{ t("x.calendar.feed_intro") }}</p>

    <p v-if="loadFailed" class="text-sm text-destructive" data-testid="x-ics-load-failed">
      {{ t("x.calendar.feed_load_failed") }}
    </p>

    <template v-else-if="status">
      <!-- Only shown right after create/rotate: this is the one time the token is visible. -->
      <div v-if="url" class="flex flex-col gap-2" data-testid="x-ics-url-box">
        <p class="text-sm text-muted-foreground">{{ t("x.calendar.feed_url_once") }}</p>
        <div class="flex gap-2">
          <Input
            :model-value="url"
            readonly
            data-testid="x-ics-url"
            @focus="($event.target as HTMLInputElement).select()"
          />
          <Button variant="outline" data-testid="x-ics-copy" @click="copy">{{ t("x.calendar.feed_copy") }}</Button>
        </div>
      </div>

      <p v-if="status.exists && !url" class="text-sm" data-testid="x-ics-active">
        {{ t("x.calendar.feed_active", { when: when(status.rotated_at ?? status.created_at) }) }}
      </p>
      <p v-else-if="!status.exists" class="text-sm text-muted-foreground" data-testid="x-ics-none">
        {{ t("x.calendar.feed_none") }}
      </p>

      <div class="flex flex-wrap gap-2">
        <Button v-if="!status.exists" :disabled="busy" data-testid="x-ics-create" @click="issue(false)">
          <MdiLoading v-if="busy" class="mr-2 inline-block animate-spin" />
          {{ t("x.calendar.feed_create") }}
        </Button>
        <template v-else>
          <Button variant="outline" size="sm" :disabled="busy" data-testid="x-ics-rotate" @click="rotateOpen = true">
            {{ t("x.calendar.feed_rotate") }}
          </Button>
          <Button variant="outline" size="sm" :disabled="busy" data-testid="x-ics-revoke" @click="revokeOpen = true">
            {{ t("x.calendar.feed_revoke") }}
          </Button>
        </template>
      </div>
    </template>

    <AlertDialog v-model:open="rotateOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t("x.calendar.feed_rotate_title") }}</AlertDialogTitle>
          <AlertDialogDescription>{{ t("x.calendar.feed_rotate_body") }}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{{ t("x.calendar.disconnect_cancel") }}</AlertDialogCancel>
          <Button :disabled="busy" data-testid="x-ics-rotate-confirm" @click="issue(true)">
            {{ t("x.calendar.feed_rotate") }}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <AlertDialog v-model:open="revokeOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t("x.calendar.feed_revoke_title") }}</AlertDialogTitle>
          <AlertDialogDescription>{{ t("x.calendar.feed_revoke_body") }}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{{ t("x.calendar.disconnect_cancel") }}</AlertDialogCancel>
          <Button variant="destructive" :disabled="busy" data-testid="x-ics-revoke-confirm" @click="revoke">
            {{ t("x.calendar.feed_revoke") }}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
