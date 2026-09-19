import { useState } from "react";
import { useTranslation } from "react-i18next";
import { updateMemberRole } from "../../api/endpoints";
import type { BusinessRole, Employee } from "../../api/types";
import { Badge, SelectMenu } from "../ds";
import { useToast } from "../ToastProvider";

const EDITABLE_ROLES: Exclude<BusinessRole, "owner">[] = ["employee", "manager", "admin"];

export function MemberRoleControl({
  employee,
  businessId,
  canChange,
  onChanged,
}: {
  employee: Employee;
  businessId: string;
  canChange: boolean;
  onChanged: () => void;
}) {
  const { t } = useTranslation("dashboard");
  const { pushToast } = useToast();
  const [busy, setBusy] = useState(false);
  if (employee.role === "owner") {
    return <Badge tone="brand">{t("employees.roles.owner")}</Badge>;
  }

  const role: Exclude<BusinessRole, "owner"> =
    employee.role === "admin" || employee.role === "manager" ? employee.role : "employee";

  if (!canChange) {
    const tone = role === "admin" ? "brand" : role === "manager" ? "info" : "neutral";
    return <Badge tone={tone}>{t(`employees.roles.${role}`)}</Badge>;
  }

  return (
    <SelectMenu
      id={`member-role-${employee.id}`}
      className="employees-role-select"
      ariaLabel={t("employees.roleAria", { name: employee.display_name })}
      value={role}
      disabled={busy}
      options={EDITABLE_ROLES.map((item) => ({
        value: item,
        label: t(`employees.roles.${item}`),
      }))}
      onChange={async (next) => {
        if (next === role) return;
        setBusy(true);
        try {
          await updateMemberRole(businessId, employee.id, next);
          onChanged();
        } catch {
          pushToast({
            title: t("employees.prompts.failed"),
            tone: "danger",
          });
        } finally {
          setBusy(false);
        }
      }}
    />
  );
}
