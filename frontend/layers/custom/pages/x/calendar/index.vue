<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import MdiLoading from "~icons/mdi/loading";
  import MdiAlertOutline from "~icons/mdi/alert-outline";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import FormCheckbox from "~/components/Form/Checkbox.vue";
  import { Button } from "@/components/ui/button";
  import { Label } from "@/components/ui/label";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
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
  import { useCustomApi } from "../../../lib/api/client";
  import type { XGcalCalendar, XGcalStatus } from "../../../lib/api/client";

  const { t } = useI18n();
  const api = useCustomApi();
  const route = useRoute();
  const router = useRouter();

  definePageMeta({
    middleware: ["auth"],
  });
  useHead({
    title: "HomeBox | " + t("x.calendar.title"),
  });

  const status = ref<XGcalStatus | null>(null);
  const loadFailed = ref(false);
  const loading = ref(true);

  const calendars = ref<XGcalCalendar[]>([]);
  const calendarsFailed = ref(false);

  // Editable copy of the settings.
  const calendarId = ref("");
  const includeCompleted = ref(true);
  const includeProjectSteps = ref(true);
  const reminderMinutes = ref("1440");

  const connecting = ref(false);
  const saving = ref(false);
  const syncing = ref(false);
  const disconnecting = ref(false);
  const disconnectOpen = ref(false);
  const removeEvents = ref(false);

  const reminderOptions = computed(() => {
    const opts = [
      { value: "0", label: t("x.calendar.reminder_none") },
      { value: "60", label: t("x.calendar.reminder_hour") },
      { value: "1440", label: t("x.calendar.reminder_day") },
      { value: "2880", label: t("x.calendar.reminder_2days") },
      { value: "10080", label: t("x.calendar.reminder_week") },
    ];
    // A value set outside the UI (e.g. 30) still shows up as the current choice.
    if (!opts.some(o => o.value === reminderMinutes.value)) {
      opts.push({
        value: reminderMinutes.value,
        label: t("x.calendar.reminder_minutes", { n: reminderMinutes.value }),
      });
    }
    return opts;
  });

  const showSettings = computed(() => !!status.value?.connected && !status.value.broken);
  const lastSync = computed(() =>
    status.value?.last_sync_at ? new Date(status.value.last_sync_at).toLocaleString() : ""
  );

  function applyStatus(s: XGcalStatus) {
    status.value = s;
    calendarId.value = s.calendar_id;
    includeCompleted.value = s.include_completed;
    includeProjectSteps.value = s.include_project_steps;
    reminderMinutes.value = String(s.reminder_minutes);
  }

  async function loadCalendars() {
    calendarsFailed.value = false;
    let resp;
    try {
      resp = await api.gcalCalendars();
    } catch {
      calendarsFailed.value = true;
      return;
    }
    if (resp.error) {
      calendarsFailed.value = true;
      // A dead token flips the status to broken: refresh so the Reconnect banner shows.
      if (resp.status === 409) {
        await loadStatus(false);
      }
      return;
    }
    calendars.value = resp.data.calendars;
  }

  async function loadStatus(withCalendars = true) {
    let resp;
    try {
      resp = await api.gcalStatus();
    } catch {
      loadFailed.value = true;
      return;
    }
    if (resp.error) {
      loadFailed.value = true;
      return;
    }
    loadFailed.value = false;
    applyStatus(resp.data);
    if (withCalendars && showSettings.value) {
      await loadCalendars();
    }
  }

  async function connect() {
    connecting.value = true;
    let resp;
    try {
      resp = await api.gcalConnect();
    } catch {
      connecting.value = false;
      toast.error(t("x.calendar.toast_failed"));
      return;
    }
    if (resp.error || !resp.data.url) {
      connecting.value = false;
      toast.error(resp.data?.error ?? t("x.calendar.toast_failed"));
      return;
    }
    // Leave the page for Google's consent screen. Google redirects back here.
    window.location.href = resp.data.url;
  }

  async function save() {
    saving.value = true;
    const body: Record<string, unknown> = {
      include_completed: includeCompleted.value,
      include_project_steps: includeProjectSteps.value,
      reminder_minutes: Number(reminderMinutes.value),
    };
    if (calendarId.value) {
      body.calendar_id = calendarId.value;
    }
    let resp;
    try {
      resp = await api.gcalSaveSettings(body);
    } catch {
      toast.error(t("x.calendar.save_failed"));
      return;
    } finally {
      saving.value = false;
    }
    if (resp.error) {
      toast.error(resp.data?.error ?? t("x.calendar.save_failed"));
      return;
    }
    applyStatus(resp.data);
    toast.success(t("x.calendar.saved"));
  }

  async function syncNow() {
    syncing.value = true;
    const resp = await api.gcalSync();
    syncing.value = false;
    if (resp.error) {
      const partial = resp.data?.result;
      if (partial && resp.data.status) {
        // Some entries synced and others failed: say what was done and what went wrong.
        toast.warning(
          t("x.calendar.synced_partial", {
            added: partial.inserted,
            updated: partial.patched,
            removed: partial.deleted,
            error: resp.data.error ?? t("x.calendar.sync_failed"),
          })
        );
        status.value = resp.data.status;
        return;
      }
      toast.error(resp.data?.error ?? t("x.calendar.sync_failed"));
      // A failed or broken sync is recorded server-side: refresh to show last error / Reconnect.
      await loadStatus(false);
      return;
    }
    const r = resp.data.result;
    if (r.inserted + r.patched + r.deleted === 0) {
      toast.success(t("x.calendar.synced_nothing"));
    } else {
      toast.success(t("x.calendar.synced", { added: r.inserted, updated: r.patched, removed: r.deleted }));
    }
    status.value = resp.data.status;
  }

  async function disconnect() {
    disconnecting.value = true;
    let resp;
    try {
      resp = await api.gcalDisconnect(removeEvents.value);
    } catch {
      toast.error(t("x.calendar.disconnect_failed"));
      return;
    } finally {
      disconnecting.value = false;
      disconnectOpen.value = false;
    }
    if (resp.error) {
      toast.error(resp.data?.error ?? t("x.calendar.disconnect_failed"));
      return;
    }
    if (resp.data.remove_skipped) {
      toast.warning(t("x.calendar.disconnected_skipped"));
    } else if (removeEvents.value) {
      toast.success(t("x.calendar.disconnected_removed", { n: resp.data.removed_events }));
    } else {
      toast.success(t("x.calendar.disconnected"));
    }
    removeEvents.value = false;
    calendars.value = [];
    await loadStatus();
  }

  // Google redirects back to /x/calendar?connected=1 or ?error=<code>.
  function handleReturn() {
    const { connected, error } = route.query;
    if (!connected && !error) return;
    if (connected) {
      toast.success(t("x.calendar.toast_connected"));
    } else if (error === "denied") {
      toast.error(t("x.calendar.toast_denied"));
    } else if (error === "state") {
      toast.error(t("x.calendar.toast_state"));
    } else if (error === "membership") {
      toast.error(t("x.calendar.toast_membership"));
    } else {
      toast.error(t("x.calendar.toast_failed"));
    }
    router.replace({ path: route.path, query: {} });
  }

  onMounted(async () => {
    handleReturn();
    try {
      await loadStatus();
    } finally {
      loading.value = false;
    }
  });
</script>

<template>
  <div>
    <BaseContainer class="flex max-w-2xl flex-col gap-4">
      <BaseSectionHeader> {{ t("x.calendar.title") }} </BaseSectionHeader>
      <p class="text-muted-foreground">{{ t("x.calendar.intro") }}</p>

      <div v-if="loading" class="flex justify-center py-8" data-testid="x-gcal-loading">
        <MdiLoading class="animate-spin" />
      </div>

      <p v-else-if="loadFailed" class="text-destructive" data-testid="x-gcal-load-failed">
        {{ t("x.calendar.load_failed") }}
      </p>

      <!-- Not configured: say exactly which variables are missing. A stored connection can
           still be disconnected locally (credentials are removed without calling Google). -->
      <div
        v-else-if="status && !status.enabled"
        class="flex flex-col gap-3 rounded-md border p-4"
        data-testid="x-gcal-not-configured"
      >
        <p class="font-medium">{{ t("x.calendar.not_configured_title") }}</p>
        <p class="text-sm text-muted-foreground">{{ t("x.calendar.not_configured_body") }}</p>
        <ul class="list-inside list-disc font-mono text-sm" data-testid="x-gcal-missing">
          <li v-for="name in status.missing" :key="name">{{ name }}</li>
        </ul>
        <template v-if="status.redirect_uri">
          <p class="text-sm text-muted-foreground">{{ t("x.calendar.redirect_uri_hint") }}</p>
          <code class="break-all rounded bg-muted p-2 text-sm">{{ status.redirect_uri }}</code>
        </template>
        <div
          v-if="status.connected"
          class="flex flex-wrap items-center justify-between gap-2 border-t pt-3"
          data-testid="x-gcal-stored-connection"
        >
          <p class="text-sm">{{ t("x.calendar.connected_as", { email: status.google_email || "—" }) }}</p>
          <Button variant="outline" size="sm" data-testid="x-gcal-disconnect" @click="disconnectOpen = true">
            {{ t("x.calendar.disconnect") }}
          </Button>
        </div>
      </div>

      <!-- Configured, not connected. -->
      <div v-else-if="status && !status.connected" class="flex flex-col gap-3" data-testid="x-gcal-disconnected">
        <div>
          <Button :disabled="connecting" data-testid="x-gcal-connect" @click="connect">
            <MdiLoading v-if="connecting" class="mr-2 inline-block animate-spin" />
            {{ connecting ? t("x.calendar.connecting") : t("x.calendar.connect") }}
          </Button>
        </div>
      </div>

      <!-- Connected. -->
      <template v-else-if="status">
        <div class="flex flex-wrap items-center justify-between gap-2 rounded-md border p-4">
          <p class="text-sm" data-testid="x-gcal-account">
            {{ t("x.calendar.connected_as", { email: status.google_email || "—" }) }}
          </p>
          <Button variant="outline" size="sm" data-testid="x-gcal-disconnect" @click="disconnectOpen = true">
            {{ t("x.calendar.disconnect") }}
          </Button>
        </div>

        <div
          v-if="status.broken"
          class="flex flex-col gap-2 rounded-md border border-destructive p-4"
          data-testid="x-gcal-broken"
        >
          <p class="flex items-center gap-2 font-medium text-destructive">
            <MdiAlertOutline /> {{ t("x.calendar.broken_title") }}
          </p>
          <p class="text-sm">{{ t("x.calendar.broken_body") }}</p>
          <p v-if="status.last_error" class="text-sm text-muted-foreground">{{ status.last_error }}</p>
          <div>
            <Button :disabled="connecting" data-testid="x-gcal-reconnect" @click="connect">
              <MdiLoading v-if="connecting" class="mr-2 inline-block animate-spin" />
              {{ t("x.calendar.reconnect") }}
            </Button>
          </div>
        </div>

        <div v-if="showSettings" class="flex flex-col gap-4" data-testid="x-gcal-settings">
          <div class="flex flex-col gap-1.5">
            <Label for="x-gcal-calendar">{{ t("x.calendar.calendar") }}</Label>
            <Select
              id="x-gcal-calendar"
              :model-value="calendarId"
              @update:model-value="val => (calendarId = String(val || ''))"
            >
              <SelectTrigger data-testid="x-gcal-calendar-select">
                <SelectValue :placeholder="t('x.calendar.calendar_placeholder')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="c in calendars" :key="c.id" :value="c.id">
                  {{ c.summary }}{{ c.primary ? " (" + status.google_email + ")" : "" }}
                </SelectItem>
              </SelectContent>
            </Select>
            <p v-if="calendarsFailed" class="text-sm text-destructive">{{ t("x.calendar.calendars_failed") }}</p>
            <p v-else class="text-sm text-muted-foreground">{{ t("x.calendar.calendar_hint") }}</p>
          </div>

          <div class="flex flex-col gap-3">
            <p class="text-sm font-medium">{{ t("x.calendar.options") }}</p>
            <FormCheckbox v-model="includeCompleted" :label="t('x.calendar.include_completed')" />
            <FormCheckbox v-model="includeProjectSteps" :label="t('x.calendar.include_project_steps')" />
            <div class="flex flex-col gap-1.5">
              <Label for="x-gcal-reminder">{{ t("x.calendar.reminder") }}</Label>
              <Select
                id="x-gcal-reminder"
                :model-value="reminderMinutes"
                @update:model-value="val => (reminderMinutes = String(val ?? '0'))"
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="o in reminderOptions" :key="o.value" :value="o.value">{{ o.label }}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div>
            <Button :disabled="saving" data-testid="x-gcal-save" @click="save">
              <MdiLoading v-if="saving" class="mr-2 inline-block animate-spin" />
              {{ t("x.calendar.save") }}
            </Button>
          </div>

          <div class="flex flex-col gap-1 rounded-md border p-4 text-sm" data-testid="x-gcal-sync-status">
            <p class="font-medium">{{ t("x.calendar.status") }}</p>
            <p v-if="!status.calendar_id" class="text-muted-foreground">{{ t("x.calendar.no_calendar_chosen") }}</p>
            <p>
              <span class="text-muted-foreground">{{ t("x.calendar.last_sync") }}:</span>
              {{ lastSync || t("x.calendar.never_synced") }}
            </p>
            <p v-if="status.last_error" class="text-destructive">
              <span class="font-medium">{{ t("x.calendar.last_error") }}:</span> {{ status.last_error }}
            </p>
            <div class="pt-2">
              <Button
                variant="outline"
                size="sm"
                :disabled="syncing || !status.calendar_id"
                data-testid="x-gcal-sync-now"
                @click="syncNow"
              >
                <MdiLoading v-if="syncing" class="mr-2 inline-block animate-spin" />
                {{ syncing ? t("x.calendar.syncing") : t("x.calendar.sync_now") }}
              </Button>
            </div>
          </div>
        </div>
      </template>
    </BaseContainer>

    <AlertDialog v-model:open="disconnectOpen">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{{ t("x.calendar.disconnect_title") }}</AlertDialogTitle>
          <AlertDialogDescription>{{ t("x.calendar.disconnect_body") }}</AlertDialogDescription>
        </AlertDialogHeader>
        <FormCheckbox v-model="removeEvents" :label="t('x.calendar.disconnect_remove')" />
        <AlertDialogFooter>
          <AlertDialogCancel>{{ t("x.calendar.disconnect_cancel") }}</AlertDialogCancel>
          <Button
            variant="destructive"
            :disabled="disconnecting"
            data-testid="x-gcal-disconnect-confirm"
            @click="disconnect"
          >
            <MdiLoading v-if="disconnecting" class="mr-2 inline-block animate-spin" />
            {{ t("x.calendar.disconnect") }}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>
