import { useQuery } from "@tanstack/react-query";
import { Scale } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui";

type SourceOffer = {
  license: string;
  sourceUrl: string;
  version: string;
  revision?: string;
  modified?: boolean;
  notice: string;
};

/**
 * SourceLink is the AGPL section 13 offer, in the interface.
 *
 * Section 13 obliges whoever runs a modified Alder to offer its source to the
 * people using it over the network. Those people are looking at this page, so
 * this is where the offer belongs; a line in a README they will never see does
 * not discharge it. The URL comes from the server's --source-url, which an
 * operator running a fork is required to set.
 */
export function SourceLink() {
  const offer = useQuery<SourceOffer>({
    queryKey: ["source"],
    staleTime: Infinity,
    retry: false,
    queryFn: async () => {
      const res = await fetch("/api/v1/source", { credentials: "same-origin" });
      if (!res.ok) throw new Error("the source offer is unavailable");
      return (await res.json()) as SourceOffer;
    },
  });

  if (!offer.data) return null;
  const { license, sourceUrl, version, modified } = offer.data;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <a
          href={sourceUrl}
          target="_blank"
          rel="noreferrer noopener"
          className="flex items-center gap-1 rounded-md px-1.5 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <Scale className="size-3.5" />
          <span className="hidden lg:inline">{license}</span>
        </a>
      </TooltipTrigger>
      <TooltipContent side="bottom">
        <p className="font-medium">Alder {version}</p>
        <p className="mt-1">
          Free software under the {license}. You are entitled to the source of
          the version you are using.
        </p>
        {modified ? (
          <p className="mt-1 text-warning-tint-foreground">
            This build was compiled from a modified working tree. If that source
            is not at the link above, the operator is not complying with the
            licence.
          </p>
        ) : null}
      </TooltipContent>
    </Tooltip>
  );
}
