import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { fetchImageObjectUrl } from "../../api/client";
import type { ScreenshotMeta } from "../../api/types";
import { fmtBytes, fmtTime } from "../../format";
import { EmptyState, Skeleton } from "../ds";

function hhmmss(timestamp: number): string {
  const date = new Date(timestamp * 1000);
  return [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map((value) => String(value).padStart(2, "0"))
    .join(":");
}

function Shot({
  meta,
  businessId,
  onOpen,
}: {
  meta: ScreenshotMeta;
  businessId: string;
  onOpen: () => void;
}) {
  const { t } = useTranslation("reports");
  const [url, setUrl] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    let objectUrl: string | null = null;
    setFailed(false);

    fetchImageObjectUrl(meta.client_uuid, businessId)
      .then((nextUrl) => {
        objectUrl = nextUrl;
        if (alive) setUrl(nextUrl);
        else URL.revokeObjectURL(nextUrl);
      })
      .catch(() => {
        if (alive) setFailed(true);
      });

    return () => {
      alive = false;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [businessId, meta.client_uuid]);

  return (
    <button
      type="button"
      className="report-shot"
      onClick={url ? onOpen : undefined}
      disabled={!url}
      aria-label={t("screenshots.alt", { time: fmtTime(meta.ts) })}
    >
      {url ? (
        <img
          className="report-shot__image"
          src={url}
          alt={t("screenshots.alt", { time: fmtTime(meta.ts) })}
        />
      ) : failed ? (
        <div className="report-shot__placeholder">{t("screenshots.unavailable")}</div>
      ) : (
        <Skeleton width="100%" height="100%" />
      )}
      <span className="report-shot__meta">
        <span>{hhmmss(meta.ts)}</span>
        <span>{meta.width}×{meta.height}</span>
      </span>
    </button>
  );
}

function Lightbox({
  shots,
  businessId,
  index,
  onIndex,
  onClose,
}: {
  shots: ScreenshotMeta[];
  businessId: string;
  index: number;
  onIndex: (index: number) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation("reports");
  const meta = shots[index];
  const [url, setUrl] = useState<string | null>(null);
  const objectUrlRef = useRef<string | null>(null);
  const hasPrevious = index > 0;
  const hasNext = index < shots.length - 1;

  useEffect(() => {
    let alive = true;
    setUrl(null);

    fetchImageObjectUrl(meta.client_uuid, businessId)
      .then((nextUrl) => {
        if (objectUrlRef.current) URL.revokeObjectURL(objectUrlRef.current);
        objectUrlRef.current = nextUrl;
        if (alive) setUrl(nextUrl);
        else URL.revokeObjectURL(nextUrl);
      })
      .catch(() => {});

    return () => {
      alive = false;
      if (objectUrlRef.current) {
        URL.revokeObjectURL(objectUrlRef.current);
        objectUrlRef.current = null;
      }
    };
  }, [businessId, meta.client_uuid]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
      if (event.key === "ArrowLeft" && hasPrevious) onIndex(index - 1);
      if (event.key === "ArrowRight" && hasNext) onIndex(index + 1);
    }

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [hasNext, hasPrevious, index, onClose, onIndex]);

  return createPortal(
    <div className="report-lightbox" onClick={onClose}>
      <button
        type="button"
        className="report-lightbox__button report-lightbox__close"
        aria-label={t("screenshots.close")}
        onClick={onClose}
      >
        ×
      </button>

      {hasPrevious && (
        <button
          type="button"
          className="report-lightbox__button report-lightbox__prev"
          aria-label={t("screenshots.prev")}
          onClick={(event) => {
            event.stopPropagation();
            onIndex(index - 1);
          }}
        >
          ‹
        </button>
      )}

      {hasNext && (
        <button
          type="button"
          className="report-lightbox__button report-lightbox__next"
          aria-label={t("screenshots.next")}
          onClick={(event) => {
            event.stopPropagation();
            onIndex(index + 1);
          }}
        >
          ›
        </button>
      )}

      <figure
        className="report-lightbox__figure"
        onClick={(event) => event.stopPropagation()}
      >
        {url ? (
          <img
            className="report-lightbox__image"
            src={url}
            alt={t("screenshots.alt", { time: fmtTime(meta.ts) })}
          />
        ) : (
          <Skeleton width={720} height={450} />
        )}
        <figcaption className="report-lightbox__caption">
          {index + 1} / {shots.length} · {fmtTime(meta.ts)} · {meta.width}×
          {meta.height} · {t("screenshots.display", { id: meta.display_id })} ·{" "}
          {fmtBytes(meta.byte_size)}
        </figcaption>
      </figure>
    </div>,
    document.body,
  );
}

export function ScreenshotGallery({
  shots,
  businessId,
}: {
  shots: ScreenshotMeta[];
  businessId: string;
}) {
  const { t } = useTranslation("reports");
  const [active, setActive] = useState<number | null>(null);
  const groups = useMemo(() => {
    const indexByUUID = new Map(
      shots.map((shot, index) => [shot.client_uuid, index] as const),
    );
    const grouped = new Map<
      string,
      { key: string; grouped: boolean; shots: ScreenshotMeta[] }
    >();
    for (const shot of shots) {
      const groupedCapture = Boolean(shot.capture_group_id);
      const key = shot.capture_group_id ?? shot.client_uuid;
      const current = grouped.get(key);
      if (current) {
        current.shots.push(shot);
      } else {
        grouped.set(key, {
          key,
          grouped: groupedCapture,
          shots: [shot],
        });
      }
    }
    return {
      items: [...grouped.values()],
      indexByUUID,
    };
  }, [shots]);

  if (shots.length === 0) {
    return (
      <EmptyState
        title={t("screenshots.empty")}
        description={t("screenshots.v1.emptyDescription")}
      />
    );
  }

  return (
    <>
      <div className="report-card__head">
        <h2 className="report-card__title">{t("screenshots.title")}</h2>
        <p className="report-card__subtitle">{t("screenshots.v1.subtitle")}</p>
      </div>

      <div className="report-gallery">
        {groups.items.map((group) =>
          group.grouped ? (
            <section className="report-capture-group" key={group.key}>
              <div className="report-capture-group__head">
                <span>
                  {t("screenshots.captureGroup", {
                    time: hhmmss(group.shots[0].ts),
                    count: group.shots.length,
                  })}
                </span>
              </div>
              <div className="report-capture-group__grid">
                {group.shots.map((shot) => (
                  <Shot
                    key={shot.client_uuid}
                    meta={shot}
                    businessId={businessId}
                    onOpen={() =>
                      setActive(groups.indexByUUID.get(shot.client_uuid) ?? 0)
                    }
                  />
                ))}
              </div>
            </section>
          ) : (
            <Shot
              key={group.shots[0].client_uuid}
              meta={group.shots[0]}
              businessId={businessId}
              onOpen={() =>
                setActive(
                  groups.indexByUUID.get(group.shots[0].client_uuid) ?? 0,
                )
              }
            />
          ),
        )}
      </div>

      {active !== null && (
        <Lightbox
          shots={shots}
          businessId={businessId}
          index={active}
          onIndex={setActive}
          onClose={() => setActive(null)}
        />
      )}
    </>
  );
}
