package main

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	dashboardEncoreWord         = regexp.MustCompile(`\bEncore\b`)
	dashboardFirstRunIntroRE    = regexp.MustCompile(`FirstRunFeatureIntro=\(\{storedKey:td,steps:ed\}\)=>\{[\s\S]*?\},VideoClip=`)
	dashboardPublicDemoCardName = regexp.MustCompile(`PublicDemo[A-Za-z]+Card=\(\)=>useDemoApp\(\)\?`)
	dashboardBrandingReplacer   = strings.NewReplacer(
		"Encore Cloud", "Pulse",
		"encore.dev/", "pulse.dev/",
		"encore.app", "pulse.app",
		"encore.service.ts", "pulse.service.ts",
		"//encore:", "//pulse:",
		"~encore/clients", "~pulse/clients",
		"encore app create", "pulse app create",
		"encore run", "pulse run",
		"encore secret set", "pulse secret set",
		"$ encore version update", "$ pulse version update",
		"git push encore", "git push pulse",
		"https://encore.cloud/book", "https://pulse.dev",
		"https://encore.cloud/pricing", "https://pulse.dev",
		"https://encore.cloud/sign-in", "https://pulse.dev",
		"https://encore.cloud", "https://pulse.dev",
		"https://a.encore.cloud/feature-request", "https://pulse.dev/discord",
		`name=encore-mcp`, `name=pulse-mcp`,
		`command:"encore"`, `command:"pulse"`,
	)
	dashboardExperienceReplacer = strings.NewReplacer(
		`Component:()=>{const hd=useProcessUpdate(),{tutorial:gd}=hd;return gd?jsxRuntimeExports.jsx(Navigate,{to:"introduction",replace:!0}):jsxRuntimeExports.jsx(Navigate,{to:"requests",replace:!0})}`,
		`Component:()=>jsxRuntimeExports.jsx(Navigate,{to:"requests",replace:!0})`,
		`{path:"introduction",element:jsxRuntimeExports.jsx(LessonContextWrapper,{}),children:[{index:!0,element:jsxRuntimeExports.jsx(IntroductionOverviewPage,{})},{path:":slug",element:jsxRuntimeExports.jsx(LessonWrapper,{})}]}`,
		`{path:"introduction",element:jsxRuntimeExports.jsx(Navigate,{to:"../requests",replace:!0})}`,
		`jsxRuntimeExports.jsxs(Tooltip,{children:[jsxRuntimeExports.jsx(TooltipTrigger,{asChild:!0,children:jsxRuntimeExports.jsxs("a",{href:"https://app.encore.cloud/"+(sd??""),target:"_blank",className:"rounded-md px-3 py-2 text-sm h-8 flex items-center gap-2 focus:outline-none hover:bg-sidebar-accent hover:text-sidebar-accent-foreground transition-colors",children:[icons.cloudGradient("size-4"),jsxRuntimeExports.jsx("span",{className:"hidden xl:inline-block font-medium",children:"Cloud Dashboard"})]})}),jsxRuntimeExports.jsx(TooltipContent,{className:"xl:hidden",children:jsxRuntimeExports.jsx("p",{children:"Cloud Dashboard"})})]})`,
		`null`,
		`jsxRuntimeExports.jsxs(Tooltip,{children:[jsxRuntimeExports.jsx(TooltipTrigger,{asChild:!0,children:jsxRuntimeExports.jsx(Button$2,{variant:"ghost",size:"icon",onClick:()=>dd(!0),className:"hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",children:jsxRuntimeExports.jsx(Rocket,{className:"size-4"})})}),jsxRuntimeExports.jsx(TooltipContent,{children:jsxRuntimeExports.jsx("p",{children:"Deploy"})})]})`,
		`null`,
		`jsxRuntimeExports.jsxs(Tooltip,{open:pd?!1:gd,children:[jsxRuntimeExports.jsx(TooltipTrigger,{asChild:!0,children:jsxRuntimeExports.jsx("div",{onMouseEnter:()=>vd(!0),onMouseLeave:()=>vd(!1),children:jsxRuntimeExports.jsxs(DropdownMenu,{open:pd,onOpenChange:bd=>{hd(bd),bd&&vd(!1)},children:[jsxRuntimeExports.jsx(DropdownMenuTrigger,{asChild:!0,children:jsxRuntimeExports.jsx(Button$2,{variant:"ghost",size:"icon",className:"hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",children:jsxRuntimeExports.jsx(CircleQuestionMark,{className:"size-4"})})}),jsxRuntimeExports.jsxs(DropdownMenuContent,{side:"bottom",align:"end",className:"w-56",collisionPadding:16,sideOffset:8,children:[jsxRuntimeExports.jsx(DropdownMenuLabel,{children:"Help & Support"}),jsxRuntimeExports.jsx(DropdownMenuSeparator,{}),jsxRuntimeExports.jsx(DropdownMenuItem,{asChild:!0,children:jsxRuntimeExports.jsx("a",{href:"https://pulse.dev/docs",target:"_blank",rel:"noopener noreferrer",children:"Documentation"})}),jsxRuntimeExports.jsx(DropdownMenuItem,{asChild:!0,children:jsxRuntimeExports.jsx("a",{href:"https://pulse.dev/discord",target:"_blank",rel:"noopener noreferrer",children:"Join Discord Community"})}),jsxRuntimeExports.jsx(DropdownMenuItem,{asChild:!0,children:jsxRuntimeExports.jsx("a",{href:"https://github.com/encoredev/encore/issues",target:"_blank",rel:"noopener noreferrer",children:"Report an Issue"})}),jsxRuntimeExports.jsx(DropdownMenuSeparator,{}),jsxRuntimeExports.jsx(DropdownMenuItem,{onClick:()=>{typeof window.Plain<"u"&&window.Plain.open()},children:"Contact Support"})]})]})})}),jsxRuntimeExports.jsx(TooltipContent,{children:jsxRuntimeExports.jsx("p",{children:"Help & Support"})})]})`,
		`null`,
		`jsxRuntimeExports.jsxs("p",{className:"text-sm text-muted-foreground leading-relaxed",children:["You can also open the"," ",jsxRuntimeExports.jsx("a",{href:"https://app.encore.cloud",className:"underline",target:"_blank",children:"Cloud Dashboard"})," ","to connect your app to GitHub and deploy by pushing to your repo."]})`,
		`null`,
		`jsxRuntimeExports.jsxs(Button$2,{variant:"outline",onClick:()=>{typeof window.Plain<"u"&&window.Plain.open()},children:[jsxRuntimeExports.jsx(IconMessageCircle,{className:"size-4"}),"Get help"]})`,
		`null`,
		`detail.field==="deploy"?jsxRuntimeExports.jsx(Wrapper,{envSlug:ed.name,children:jsxRuntimeExports.jsx("div",{className:"flex flex-col items-center justify-center py-12",children:jsxRuntimeExports.jsxs(Empty,{children:[jsxRuntimeExports.jsxs(EmptyHeader,{children:[jsxRuntimeExports.jsx(EmptyMedia,{variant:"icon",children:jsxRuntimeExports.jsx(Activity,{})}),jsxRuntimeExports.jsx(EmptyTitle,{children:"Deploy your app to view Traces"}),jsxRuntimeExports.jsx(EmptyDescription,{children:"You're almost there! Deploy to unlock distributed tracing and see how requests flow through your system."})]}),jsxRuntimeExports.jsxs(HoverCard,{children:[jsxRuntimeExports.jsx(HoverCardTrigger,{asChild:!0,children:jsxRuntimeExports.jsxs(Button$2,{variant:"outline",className:"mt-4",children:[jsxRuntimeExports.jsx(IconEye,{className:"h-4 w-4"}),"Sneak Peek"]})}),jsxRuntimeExports.jsx(HoverCardContent,{className:"w-[600px] p-0",side:"bottom",align:"center",children:jsxRuntimeExports.jsx("video",{playsInline:!0,autoPlay:!0,loop:!0,controls:!0,muted:!0,className:"w-full h-full rounded-md",children:jsxRuntimeExports.jsx("source",{src:"/assets/videos/tracing.mp4",type:"video/mp4"})})})]})]})})}):`,
		`detail.field==="deploy"?jsxRuntimeExports.jsx(PageContent,{className:"flex items-center justify-center min-h-[400px]",children:jsxRuntimeExports.jsx(Empty,{children:jsxRuntimeExports.jsxs(EmptyHeader,{children:[jsxRuntimeExports.jsx(EmptyTitle,{children:"No traces yet"}),jsxRuntimeExports.jsx(EmptyDescription,{children:"Make some API calls and captured traces will show up here."})]})})}):`,
	)
)

func rewriteDashboardBranding(text string) string {
	text = dashboardBrandingReplacer.Replace(text)
	return dashboardEncoreWord.ReplaceAllString(text, "Pulse")
}

func rewriteDashboardAsset(name string, data []byte) []byte {
	if !isDashboardTextAsset(name) {
		return data
	}
	text := rewriteDashboardBranding(string(data))
	if filepath.Ext(name) == ".js" {
		text = rewriteDashboardExperience(text)
	}
	return []byte(text)
}

func isDashboardTextAsset(name string) bool {
	switch ext := filepath.Ext(name); ext {
	case ".html", ".js", ".json", ".svg":
		return true
	}
	return strings.HasSuffix(name, ".webmanifest")
}

func rewriteDashboardExperience(text string) string {
	text = dashboardExperienceReplacer.Replace(text)
	text = dashboardFirstRunIntroRE.ReplaceAllString(text, `FirstRunFeatureIntro=()=>null,VideoClip=`)
	text = stripPublicDemoCards(text)
	return text
}

func stripPublicDemoCards(text string) string {
	matches := dashboardPublicDemoCardName.FindAllStringIndex(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start := matches[i][0]
		eq := strings.IndexByte(text[start:], '=')
		end := strings.Index(text[start:], `:null`)
		if eq <= 0 || end < 0 {
			continue
		}
		name := text[start : start+eq]
		text = text[:start] + name + `=()=>null` + text[start+end+len(`:null`):]
	}
	return text
}
