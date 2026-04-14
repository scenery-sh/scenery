package main

import (
	"strings"
	"testing"
)

func TestRewriteDashboardBranding(t *testing.T) {
	input := strings.Join([]string{
		"Welcome to Encore",
		"Encore Cloud",
		"encore.app",
		`import "encore.dev/config"`,
		"//encore:api public",
		"~encore/clients",
		"encore run",
		"encore secret set --type local MySecretKey",
		"$ encore version update",
		"encore-dark",
		"EncoreCloudAppRun",
	}, "\n")

	got := rewriteDashboardBranding(input)

	for _, want := range []string{
		"Welcome to Pulse",
		"Pulse",
		"pulse.app",
		`import "pulse.dev/config"`,
		"//pulse:api public",
		"~pulse/clients",
		"pulse run",
		"pulse secret set --type local MySecretKey",
		"$ pulse version update",
		"encore-dark",
		"EncoreCloudAppRun",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rewriteDashboardBranding missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Welcome to Encore") {
		t.Fatalf("rewriteDashboardBranding did not replace visible brand copy:\n%s", got)
	}
}

func TestRewriteDashboardAsset(t *testing.T) {
	js := rewriteDashboardAsset("devdash_static/assets/index.js", []byte(`const title = "Welcome to Encore";`))
	if !strings.Contains(string(js), "Welcome to Pulse") {
		t.Fatalf("expected js asset branding rewrite, got %q", string(js))
	}

	png := rewriteDashboardAsset("devdash_static/assets/logo.png", []byte("raw-binary"))
	if string(png) != "raw-binary" {
		t.Fatalf("expected binary asset to remain unchanged, got %q", string(png))
	}
}

func TestRewriteDashboardExperience(t *testing.T) {
	input := strings.Join([]string{
		`Component:()=>{const hd=useProcessUpdate(),{tutorial:gd}=hd;return gd?jsxRuntimeExports.jsx(Navigate,{to:"introduction",replace:!0}):jsxRuntimeExports.jsx(Navigate,{to:"requests",replace:!0})}`,
		`{path:"introduction",element:jsxRuntimeExports.jsx(LessonContextWrapper,{}),children:[{index:!0,element:jsxRuntimeExports.jsx(IntroductionOverviewPage,{})},{path:":slug",element:jsxRuntimeExports.jsx(LessonWrapper,{})}]}`,
		`FirstRunFeatureIntro=({storedKey:td,steps:ed})=>{const nd=useConn();return jsxRuntimeExports.jsx(Dialog,{open:sd,onOpenChange:()=>{},children:"promo"})},VideoClip=`,
		`PublicDemoTracingCard=()=>useDemoApp()?jsxRuntimeExports.jsx("div",{}):null,const next=1`,
		`jsxRuntimeExports.jsxs(Tooltip,{children:[jsxRuntimeExports.jsx(TooltipTrigger,{asChild:!0,children:jsxRuntimeExports.jsxs("a",{href:"https://app.encore.cloud/"+(sd??""),target:"_blank",className:"rounded-md px-3 py-2 text-sm h-8 flex items-center gap-2 focus:outline-none hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors",children:[icons.cloudGradient("size-4"),jsxRuntimeExports.jsx("span",{className:"hidden xl:inline-block font-medium",children:"Cloud Dashboard"})]})}),jsxRuntimeExports.jsx(TooltipContent,{className:"xl:hidden",children:jsxRuntimeExports.jsx("p",{children:"Cloud Dashboard"})})]})`,
		`jsxRuntimeExports.jsxs(Tooltip,{children:[jsxRuntimeExports.jsx(TooltipTrigger,{asChild:!0,children:jsxRuntimeExports.jsx(Button$2,{variant:"ghost",size:"icon",onClick:()=>dd(!0),className:"hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",children:jsxRuntimeExports.jsx(Rocket,{className:"size-4"})})}),jsxRuntimeExports.jsx(TooltipContent,{children:jsxRuntimeExports.jsx("p",{children:"Deploy"})})]})`,
		`jsxRuntimeExports.jsxs(Tooltip,{open:pd?!1:gd,children:[jsxRuntimeExports.jsx(TooltipTrigger,{asChild:!0,children:jsxRuntimeExports.jsx("div",{onMouseEnter:()=>vd(!0),onMouseLeave:()=>vd(!1),children:jsxRuntimeExports.jsxs(DropdownMenu,{open:pd,onOpenChange:bd=>{hd(bd),bd&&vd(!1)},children:[jsxRuntimeExports.jsx(DropdownMenuTrigger,{asChild:!0,children:jsxRuntimeExports.jsx(Button$2,{variant:"ghost",size:"icon",className:"hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",children:jsxRuntimeExports.jsx(CircleQuestionMark,{className:"size-4"})})}),jsxRuntimeExports.jsxs(DropdownMenuContent,{side:"bottom",align:"end",className:"w-56",collisionPadding:16,sideOffset:8,children:[jsxRuntimeExports.jsx(DropdownMenuLabel,{children:"Help & Support"}),jsxRuntimeExports.jsx(DropdownMenuSeparator,{}),jsxRuntimeExports.jsx(DropdownMenuItem,{asChild:!0,children:jsxRuntimeExports.jsx("a",{href:"https://pulse.dev/docs",target:"_blank",rel:"noopener noreferrer",children:"Documentation"})}),jsxRuntimeExports.jsx(DropdownMenuItem,{asChild:!0,children:jsxRuntimeExports.jsx("a",{href:"https://pulse.dev/discord",target:"_blank",rel:"noopener noreferrer",children:"Join Discord Community"})}),jsxRuntimeExports.jsx(DropdownMenuItem,{asChild:!0,children:jsxRuntimeExports.jsx("a",{href:"https://github.com/encoredev/encore/issues",target:"_blank",rel:"noopener noreferrer",children:"Report an Issue"})}),jsxRuntimeExports.jsx(DropdownMenuSeparator,{}),jsxRuntimeExports.jsx(DropdownMenuItem,{onClick:()=>{typeof window.Plain<"u"&&window.Plain.open()},children:"Contact Support"})]})]})})}),jsxRuntimeExports.jsx(TooltipContent,{children:jsxRuntimeExports.jsx("p",{children:"Help & Support"})})]})`,
		`jsxRuntimeExports.jsxs("p",{className:"text-sm text-muted-foreground leading-relaxed",children:["You can also open the"," ",jsxRuntimeExports.jsx("a",{href:"https://app.encore.cloud",className:"underline",target:"_blank",children:"Cloud Dashboard"})," ","to connect your app to GitHub and deploy by pushing to your repo."]})`,
		`jsxRuntimeExports.jsxs(Button$2,{variant:"outline",onClick:()=>{typeof window.Plain<"u"&&window.Plain.open()},children:[jsxRuntimeExports.jsx(IconMessageCircle,{className:"size-4"}),"Get help"]})`,
		`detail.field==="deploy"?jsxRuntimeExports.jsx(Wrapper,{envSlug:ed.name,children:jsxRuntimeExports.jsx("div",{className:"flex flex-col items-center justify-center py-12",children:jsxRuntimeExports.jsxs(Empty,{children:[jsxRuntimeExports.jsxs(EmptyHeader,{children:[jsxRuntimeExports.jsx(EmptyMedia,{variant:"icon",children:jsxRuntimeExports.jsx(Activity,{})}),jsxRuntimeExports.jsx(EmptyTitle,{children:"Deploy your app to view Traces"}),jsxRuntimeExports.jsx(EmptyDescription,{children:"You're almost there! Deploy to unlock distributed tracing and see how requests flow through your system."})]}),jsxRuntimeExports.jsxs(HoverCard,{children:[jsxRuntimeExports.jsx(HoverCardTrigger,{asChild:!0,children:jsxRuntimeExports.jsxs(Button$2,{variant:"outline",className:"mt-4",children:[jsxRuntimeExports.jsx(IconEye,{className:"h-4 w-4"}),"Sneak Peek"]})}),jsxRuntimeExports.jsx(HoverCardContent,{className:"w-[600px] p-0",side:"bottom",align:"center",children:jsxRuntimeExports.jsx("video",{playsInline:!0,autoPlay:!0,loop:!0,controls:!0,muted:!0,className:"w-full h-full rounded-md",children:jsxRuntimeExports.jsx("source",{src:"/assets/videos/tracing.mp4",type:"video/mp4"})})})]})]})})}):`,
	}, "\n")

	got := rewriteDashboardExperience(input)

	for _, unwanted := range []string{
		`to:"introduction"`,
		`LessonContextWrapper`,
		`FirstRunFeatureIntro=({storedKey:td,steps:ed})`,
		`PublicDemoTracingCard=()=>useDemoApp()?`,
		`children:"Cloud Dashboard"`,
		`href:"https://app.encore.cloud`,
		`children:"Deploy"`,
		`CircleQuestionMark`,
		`Help & Support`,
		`Contact Support`,
		`Get help`,
		`Deploy your app to view Traces`,
		`Sneak Peek`,
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rewriteDashboardExperience still contains %q in:\n%s", unwanted, got)
		}
	}
	for _, wanted := range []string{
		`Component:()=>jsxRuntimeExports.jsx(Navigate,{to:"requests",replace:!0})`,
		`{path:"introduction",element:jsxRuntimeExports.jsx(Navigate,{to:"../requests",replace:!0})}`,
		`FirstRunFeatureIntro=()=>null,VideoClip=`,
		`PublicDemoTracingCard=()=>null`,
		`null`,
		`EmptyTitle,{children:"No traces yet"}`,
	} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("rewriteDashboardExperience missing %q in:\n%s", wanted, got)
		}
	}
}
