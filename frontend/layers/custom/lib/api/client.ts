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

// ---- ICS feed (G4) ----
export type XIcsStatus = {
  exists: boolean;
  created_at: string | null;
  rotated_at: string | null;
};

/** Returned once when a feed token is created or rotated; `url` contains the secret token. */
export type XIcsIssued = XIcsStatus & { url: string };

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

  // ---- ICS feed (G4) ----
  icsStatus() {
    return this.http.get<XIcsStatus>({ url: route("/x/calendar/feed") });
  }

  /** Creates the feed token, or rotates it (the old URL stops working). */
  icsIssue() {
    return this.http.post<undefined, XIcsIssued & XError>({ url: route("/x/calendar/feed") });
  }

  icsRevoke() {
    return this.http.delete<void>({ url: route("/x/calendar/feed") });
  }
}

/** Custom API client sharing upstream's authenticated request pipeline (auth, X-Tenant, 401 handling). */
export function useCustomApi(): CustomApi {
  const user = useUserApi();
  return new CustomApi(user.http, user.attachmentToken);
}
