// Typed client for the fork's /api/v1/x endpoints. Hand-written on purpose: custom handlers
// are not in upstream's swagger, so frontend/lib/api/types is never regenerated (R7).
import { BaseAPI, route } from "~~/lib/api/base";

export type XPing = {
  ok: boolean;
  groupId: string;
  schema: string;
};

// ---- Google Calendar types (G1) ----
export type XGcalStatus = {
  enabled: boolean;
  missing: string[];
  redirect_uri: string;
  connected: boolean;
  broken: boolean;
  google_email: string;
  calendar_id: string;
  calendar_summary: string;
  include_completed: boolean;
  include_project_steps: boolean;
  reminder_minutes: number;
  last_sync_at: string | null;
  last_error: string;
};

export type XGcalCalendar = {
  id: string;
  summary: string;
  accessRole: string;
  primary?: boolean;
};

export type XGcalSettings = {
  calendar_id?: string;
  include_completed?: boolean;
  include_project_steps?: boolean;
  reminder_minutes?: number;
};

export type XGcalDisconnectResult = {
  removed_events: number;
  remove_skipped: boolean;
};

// ---- Google Calendar sync (G2) ----
export type XGcalSyncResult = {
  result: { inserted: number; patched: number; deleted: number; relinked: number; unchanged: number };
  status: XGcalStatus;
};

/** Error body shape of the custom API (same as upstream's). */
export type XError = { error?: string };

export class CustomApi extends BaseAPI {
  ping() {
    return this.http.get<XPing>({ url: route("/x/ping") });
  }

  // ---- Google Calendar (G1) ----
  gcalStatus() {
    return this.http.get<XGcalStatus>({ url: route("/x/gcal/status") });
  }

  /** Returns the Google consent URL to send the browser to. */
  gcalConnect() {
    return this.http.post<undefined, { url: string } & XError>({ url: route("/x/gcal/connect") });
  }

  gcalCalendars() {
    return this.http.get<{ calendars: XGcalCalendar[] } & XError>({ url: route("/x/gcal/calendars") });
  }

  gcalSaveSettings(body: XGcalSettings) {
    return this.http.put<XGcalSettings, XGcalStatus & XError>({ url: route("/x/gcal/settings"), body });
  }

  gcalDisconnect(removeEvents: boolean) {
    return this.http.post<{ remove_events: boolean }, XGcalDisconnectResult & XError>({
      url: route("/x/gcal/disconnect"),
      body: { remove_events: removeEvents },
    });
  }

  // ---- Google Calendar sync (G2) ----
  /** "Sync now": reconcile this group's calendar immediately. */
  gcalSync() {
    return this.http.post<undefined, XGcalSyncResult & XError>({ url: route("/x/gcal/sync") });
  }
}

/** Custom API client sharing upstream's authenticated request pipeline (auth, X-Tenant, 401 handling). */
export function useCustomApi(): CustomApi {
  const user = useUserApi();
  return new CustomApi(user.http, user.attachmentToken);
}
