// Typed client for the fork's /api/v1/x endpoints. Hand-written on purpose: custom handlers
// are not in upstream's swagger, so frontend/lib/api/types is never regenerated (R7).
import { BaseAPI, route } from "~~/lib/api/base";

export type XPing = {
  ok: boolean;
  groupId: string;
  schema: string;
};

// ---- Projects (P1-P4) ----------------------------------------------------------------
// Dates are "YYYY-MM-DD" strings (or RFC3339 from the server; use toDateInput in lib/projects.ts).
export type XProjectStatus = "idea" | "planning" | "in_progress" | "on_hold" | "done" | "cancelled";
export type XStepStatus = "todo" | "doing" | "blocked" | "done" | "skipped";

export type XProjectStep = {
  id: string;
  projectId: string;
  position: number;
  title: string;
  notes: string;
  status: XStepStatus;
  dueDate: string | null;
  estCost: number | null;
  actualCost: number | null;
  url: string | null;
  entityId: string | null;
  logAsMaintenance: boolean;
  maintenanceEntryId: string | null;
  completedAt: string | null;
};

export type XProject = {
  id: string;
  groupId: string;
  name: string;
  description: string;
  status: XProjectStatus;
  priority: number;
  targetDate: string | null;
  budget: number | null;
  startedAt: string | null;
  completedAt: string | null;
  createdAt: string;
  updatedAt: string;
  steps: XProjectStep[];
  entityIds: string[];
};

export type XProjectInput = {
  name: string;
  description: string;
  status?: XProjectStatus;
  priority: number;
  targetDate: string | null;
  budget: number | null;
};

export type XStepInput = {
  title: string;
  notes: string;
  status?: XStepStatus;
  dueDate: string | null;
  estCost: number | null;
  actualCost: number | null;
  url: string | null;
  entityId: string | null;
  logAsMaintenance: boolean;
};

export type XCompleteStepInput = {
  actualCost?: number | null;
  logAsMaintenance?: boolean;
};

// ---- Client ---------------------------------------------------------------------------
export class CustomApi extends BaseAPI {
  ping() {
    return this.http.get<XPing>({ url: route("/x/ping") });
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

  addStep(projectId: string, body: XStepInput) {
    return this.http.post<XStepInput, XProjectStep>({ url: route(`/x/projects/${projectId}/steps`), body });
  }

  updateStep(projectId: string, stepId: string, body: XStepInput) {
    return this.http.put<XStepInput, XProjectStep>({ url: route(`/x/projects/${projectId}/steps/${stepId}`), body });
  }

  deleteStep(projectId: string, stepId: string) {
    return this.http.delete<void>({ url: route(`/x/projects/${projectId}/steps/${stepId}`) });
  }

  reorderSteps(projectId: string, stepIds: string[]) {
    return this.http.patch<{ stepIds: string[] }, void>({
      url: route(`/x/projects/${projectId}/steps/order`),
      body: { stepIds },
    });
  }

  completeStep(projectId: string, stepId: string, body: XCompleteStepInput) {
    return this.http.post<XCompleteStepInput, XProjectStep>({
      url: route(`/x/projects/${projectId}/steps/${stepId}/complete`),
      body,
    });
  }

  setProjectEntities(projectId: string, entityIds: string[]) {
    return this.http.put<{ entityIds: string[] }, void>({
      url: route(`/x/projects/${projectId}/entities`),
      body: { entityIds },
    });
  }

  projectsForEntity(entityId: string) {
    return this.http.get<XProject[]>({ url: route(`/x/entities/${entityId}/projects`) });
  }
}

/** Custom API client sharing upstream's authenticated request pipeline (auth, X-Tenant, 401 handling). */
export function useCustomApi(): CustomApi {
  const user = useUserApi();
  return new CustomApi(user.http, user.attachmentToken);
}
