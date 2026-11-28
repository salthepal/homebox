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
  /** Set (with a 5xx status) when some entries synced and others failed. */
  error?: string;
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

// ---- Projects (P1-P4) ----------------------------------------------------------------
// Dates are "YYYY-MM-DD" strings (or RFC3339 from the server; use toDateInput in lib/projects.ts).
export type XProjectStatus = "idea" | "planning" | "in_progress" | "on_hold" | "done" | "cancelled";
export type XStepStatus = "todo" | "doing" | "blocked" | "done" | "skipped";

export type XProjectStep = {
  id: string;
  project_id: string;
  position: number;
  title: string;
  notes: string;
  status: XStepStatus;
  due_date: string | null;
  est_cost: number | null;
  actual_cost: number | null;
  url: string | null;
  entity_id: string | null;
  log_as_maintenance: boolean;
  maintenance_entry_id: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
};

export type XProject = {
  id: string;
  name: string;
  description: string;
  status: XProjectStatus;
  priority: number;
  target_date: string | null;
  budget: number | null;
  started_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
  steps: XProjectStep[];
  entity_ids: string[];
  entities: Array<{ id: string; name: string }>;
  progress: { done: number; total: number };
  est_cost_total: number;
  actual_cost_total: number;
  all_steps_complete: boolean;
};

export type XProjectInput = {
  name: string;
  description: string;
  status?: XProjectStatus;
  priority: number;
  target_date: string | null;
  budget: number | null;
};

export type XStepInput = {
  title: string;
  notes: string;
  status?: XStepStatus;
  due_date: string | null;
  est_cost: number | null;
  actual_cost: number | null;
  url: string | null;
  entity_id: string | null;
  log_as_maintenance: boolean;
};

export type XCompleteStepInput = {
  actual_cost?: number | null;
  log_as_maintenance?: boolean;
};

// ---- Client ---------------------------------------------------------------------------
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

  // -- projects --
  listProjects(status?: XProjectStatus) {
    return this.http.get<XProject[]>({ url: route("/x/projects", status ? { status } : {}) });
  }

  getProject(id: string) {
    return this.http.get<XProject>({ url: route(`/x/projects/${id}`) });
  }

  createProject(body: XProjectInput) {
    return this.http.post<XProjectInput, XProject>({ url: route("/x/projects"), body });
  }

  updateProject(id: string, body: XProjectInput) {
    return this.http.put<XProjectInput, XProject>({ url: route(`/x/projects/${id}`), body });
  }

  deleteProject(id: string) {
    return this.http.delete<void>({ url: route(`/x/projects/${id}`) });
  }

  addStep(project_id: string, body: XStepInput) {
    return this.http.post<XStepInput, XProjectStep>({ url: route(`/x/projects/${project_id}/steps`), body });
  }

  updateStep(project_id: string, stepId: string, body: XStepInput) {
    return this.http.put<XStepInput, XProjectStep>({ url: route(`/x/projects/${project_id}/steps/${stepId}`), body });
  }

  deleteStep(project_id: string, stepId: string) {
    return this.http.delete<void>({ url: route(`/x/projects/${project_id}/steps/${stepId}`) });
  }

  reorderSteps(project_id: string, step_ids: string[]) {
    return this.http.patch<{ step_ids: string[] }, XProject>({
      url: route(`/x/projects/${project_id}/steps/order`),
      body: { step_ids },
    });
  }

  completeStep(project_id: string, stepId: string, body: XCompleteStepInput) {
    return this.http.post<XCompleteStepInput, XProjectStep>({
      url: route(`/x/projects/${project_id}/steps/${stepId}/complete`),
      body,
    });
  }

  setProjectEntities(project_id: string, entity_ids: string[]) {
    return this.http.put<{ entity_ids: string[] }, XProject>({
      url: route(`/x/projects/${project_id}/entities`),
      body: { entity_ids },
    });
  }

  projectsForEntity(entity_id: string) {
    return this.http.get<XProject[]>({ url: route(`/x/entities/${entity_id}/projects`) });
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

  /** Creates the feed token, or rotates it (the old URL stops working). Group owners only. */
  icsIssue() {
    return this.http.post<undefined, XIcsIssued & XError>({ url: route("/x/calendar/feed") });
  }

  /** Revokes the feed token. Group owners only. */
  icsRevoke() {
    return this.http.delete<void>({ url: route("/x/calendar/feed") });
  }
}

/** Custom API client sharing upstream's authenticated request pipeline (auth, X-Tenant, 401 handling). */
export function useCustomApi(): CustomApi {
  const user = useUserApi();
  return new CustomApi(user.http, user.attachmentToken);
}
