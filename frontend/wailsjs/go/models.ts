export namespace main {
	
	export class AppInfo {
	    dbPath: string;
	    projectsRoot: string;
	    ingesting: boolean;
	    apiBase: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dbPath = source["dbPath"];
	        this.projectsRoot = source["projectsRoot"];
	        this.ingesting = source["ingesting"];
	        this.apiBase = source["apiBase"];
	    }
	}
	export class EpisodeStatsBucket {
	    label: string;
	    episodeCount: number;
	    endedCount: number;
	    avgToolCallsBeforeEdit: number;
	    avgFilesReadBeforeEdit: number;
	    avgInputTokens: number;
	    avgOutputTokens: number;
	    avgDurationMs: number;
	    totalBashCalls: number;
	    totalSearchCalls: number;
	    totalSubagentCalls: number;
	
	    static createFrom(source: any = {}) {
	        return new EpisodeStatsBucket(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.episodeCount = source["episodeCount"];
	        this.endedCount = source["endedCount"];
	        this.avgToolCallsBeforeEdit = source["avgToolCallsBeforeEdit"];
	        this.avgFilesReadBeforeEdit = source["avgFilesReadBeforeEdit"];
	        this.avgInputTokens = source["avgInputTokens"];
	        this.avgOutputTokens = source["avgOutputTokens"];
	        this.avgDurationMs = source["avgDurationMs"];
	        this.totalBashCalls = source["totalBashCalls"];
	        this.totalSearchCalls = source["totalSearchCalls"];
	        this.totalSubagentCalls = source["totalSubagentCalls"];
	    }
	}

}

export namespace store {
	
	export class BashBreakdownRow {
	    program: string;
	    action: string;
	    count: number;
	    errorCount: number;
	    sessions: number;
	
	    static createFrom(source: any = {}) {
	        return new BashBreakdownRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.program = source["program"];
	        this.action = source["action"];
	        this.count = source["count"];
	        this.errorCount = source["errorCount"];
	        this.sessions = source["sessions"];
	    }
	}
	export class BashCommandSample {
	    id: number;
	    sessionId: string;
	    seq: number;
	    ts: string;
	    command: string;
	    description: string;
	    resultBytes: number;
	    isError: boolean;
	    episodeId: number;
	
	    static createFrom(source: any = {}) {
	        return new BashCommandSample(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.seq = source["seq"];
	        this.ts = source["ts"];
	        this.command = source["command"];
	        this.description = source["description"];
	        this.resultBytes = source["resultBytes"];
	        this.isError = source["isError"];
	        this.episodeId = source["episodeId"];
	    }
	}
	export class Episode {
	    id: number;
	    sessionId: string;
	    seq: number;
	    userMessageId: number;
	    firstEditToolCallId: number;
	    ended: boolean;
	    userPromptExcerpt: string;
	    startedAt: string;
	    firstEditAt: string;
	    durationMs: number;
	    toolCallCount: number;
	    filesReadCount: number;
	    filesReadJson: string;
	    bashCount: number;
	    searchCount: number;
	    subagentCount: number;
	    inputTokens: number;
	    outputTokens: number;
	    cacheReadTokens: number;
	    cacheCreationTokens: number;
	    editFilePath: string;
	    bytesReadBeforeEdit: number;
	    intermediateArtifactCount: number;
	    intermediateArtifactsJson: string;
	    costUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new Episode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.seq = source["seq"];
	        this.userMessageId = source["userMessageId"];
	        this.firstEditToolCallId = source["firstEditToolCallId"];
	        this.ended = source["ended"];
	        this.userPromptExcerpt = source["userPromptExcerpt"];
	        this.startedAt = source["startedAt"];
	        this.firstEditAt = source["firstEditAt"];
	        this.durationMs = source["durationMs"];
	        this.toolCallCount = source["toolCallCount"];
	        this.filesReadCount = source["filesReadCount"];
	        this.filesReadJson = source["filesReadJson"];
	        this.bashCount = source["bashCount"];
	        this.searchCount = source["searchCount"];
	        this.subagentCount = source["subagentCount"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheReadTokens = source["cacheReadTokens"];
	        this.cacheCreationTokens = source["cacheCreationTokens"];
	        this.editFilePath = source["editFilePath"];
	        this.bytesReadBeforeEdit = source["bytesReadBeforeEdit"];
	        this.intermediateArtifactCount = source["intermediateArtifactCount"];
	        this.intermediateArtifactsJson = source["intermediateArtifactsJson"];
	        this.costUsd = source["costUsd"];
	    }
	}
	export class EpisodeBucket {
	    label: string;
	    min: number;
	    max: number;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new EpisodeBucket(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.count = source["count"];
	    }
	}
	export class FileBlastRadius {
	    filePath: string;
	    readCount: number;
	    editCount: number;
	    sessions: number;
	    projects: number;
	    episodes: number;
	    avgSiblingEdits: number;
	    firstSeen: string;
	    lastSeen: string;
	
	    static createFrom(source: any = {}) {
	        return new FileBlastRadius(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filePath = source["filePath"];
	        this.readCount = source["readCount"];
	        this.editCount = source["editCount"];
	        this.sessions = source["sessions"];
	        this.projects = source["projects"];
	        this.episodes = source["episodes"];
	        this.avgSiblingEdits = source["avgSiblingEdits"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	    }
	}
	export class FileTouchStat {
	    filePath: string;
	    readCount: number;
	    editCount: number;
	    totalCount: number;
	    sessions: number;
	
	    static createFrom(source: any = {}) {
	        return new FileTouchStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filePath = source["filePath"];
	        this.readCount = source["readCount"];
	        this.editCount = source["editCount"];
	        this.totalCount = source["totalCount"];
	        this.sessions = source["sessions"];
	    }
	}
	export class Message {
	    id: number;
	    sessionId: string;
	    uuid: string;
	    parentUuid: string;
	    seq: number;
	    role: string;
	    eventType: string;
	    eventSubtype: string;
	    ts: string;
	    model: string;
	    textContent: string;
	    inputTokens: number;
	    outputTokens: number;
	    cacheReadTokens: number;
	    cacheCreationTokens: number;
	    isSidechain: boolean;
	    isMeta: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.uuid = source["uuid"];
	        this.parentUuid = source["parentUuid"];
	        this.seq = source["seq"];
	        this.role = source["role"];
	        this.eventType = source["eventType"];
	        this.eventSubtype = source["eventSubtype"];
	        this.ts = source["ts"];
	        this.model = source["model"];
	        this.textContent = source["textContent"];
	        this.inputTokens = source["inputTokens"];
	        this.outputTokens = source["outputTokens"];
	        this.cacheReadTokens = source["cacheReadTokens"];
	        this.cacheCreationTokens = source["cacheCreationTokens"];
	        this.isSidechain = source["isSidechain"];
	        this.isMeta = source["isMeta"];
	    }
	}
	export class Project {
	    slug: string;
	    cwd: string;
	    displayName: string;
	    sessionCount: number;
	    fileCount: number;
	    lastSeenAt: string;
	    totalMessages: number;
	    totalToolCalls: number;
	    totalEdits: number;
	    totalInputTokens: number;
	    totalOutputTokens: number;
	    totalCacheReadTokens: number;
	    totalCacheCreationTokens: number;
	    estimatedCostUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.slug = source["slug"];
	        this.cwd = source["cwd"];
	        this.displayName = source["displayName"];
	        this.sessionCount = source["sessionCount"];
	        this.fileCount = source["fileCount"];
	        this.lastSeenAt = source["lastSeenAt"];
	        this.totalMessages = source["totalMessages"];
	        this.totalToolCalls = source["totalToolCalls"];
	        this.totalEdits = source["totalEdits"];
	        this.totalInputTokens = source["totalInputTokens"];
	        this.totalOutputTokens = source["totalOutputTokens"];
	        this.totalCacheReadTokens = source["totalCacheReadTokens"];
	        this.totalCacheCreationTokens = source["totalCacheCreationTokens"];
	        this.estimatedCostUsd = source["estimatedCostUsd"];
	    }
	}
	export class SearchHit {
	    messageId: number;
	    sessionId: string;
	    projectSlug: string;
	    role: string;
	    ts: string;
	    snippet: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messageId = source["messageId"];
	        this.sessionId = source["sessionId"];
	        this.projectSlug = source["projectSlug"];
	        this.role = source["role"];
	        this.ts = source["ts"];
	        this.snippet = source["snippet"];
	    }
	}
	export class Session {
	    sessionId: string;
	    projectSlug: string;
	    cwd: string;
	    gitBranch: string;
	    aiTitle: string;
	    startedAt: string;
	    endedAt: string;
	    messageCount: number;
	    toolCallCount: number;
	    editCount: number;
	    episodeCount: number;
	    totalInputTokens: number;
	    totalOutputTokens: number;
	    totalCacheReadTokens: number;
	    totalCacheCreationTokens: number;
	    sourcePath: string;
	    sizeBytes: number;
	    bytesIngested: number;
	    primaryModel: string;
	    estimatedCostUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.projectSlug = source["projectSlug"];
	        this.cwd = source["cwd"];
	        this.gitBranch = source["gitBranch"];
	        this.aiTitle = source["aiTitle"];
	        this.startedAt = source["startedAt"];
	        this.endedAt = source["endedAt"];
	        this.messageCount = source["messageCount"];
	        this.toolCallCount = source["toolCallCount"];
	        this.editCount = source["editCount"];
	        this.episodeCount = source["episodeCount"];
	        this.totalInputTokens = source["totalInputTokens"];
	        this.totalOutputTokens = source["totalOutputTokens"];
	        this.totalCacheReadTokens = source["totalCacheReadTokens"];
	        this.totalCacheCreationTokens = source["totalCacheCreationTokens"];
	        this.sourcePath = source["sourcePath"];
	        this.sizeBytes = source["sizeBytes"];
	        this.bytesIngested = source["bytesIngested"];
	        this.primaryModel = source["primaryModel"];
	        this.estimatedCostUsd = source["estimatedCostUsd"];
	    }
	}
	export class ToolBreakdown {
	    name: string;
	    count: number;
	    modifyingCount: number;
	    errorCount: number;
	    totalResultBytes: number;
	
	    static createFrom(source: any = {}) {
	        return new ToolBreakdown(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.count = source["count"];
	        this.modifyingCount = source["modifyingCount"];
	        this.errorCount = source["errorCount"];
	        this.totalResultBytes = source["totalResultBytes"];
	    }
	}
	export class ToolCall {
	    id: number;
	    sessionId: string;
	    messageId: number;
	    seq: number;
	    toolUseId: string;
	    name: string;
	    inputJson: string;
	    filePath: string;
	    isModifying: boolean;
	    isRead: boolean;
	    isBash: boolean;
	    isSearch: boolean;
	    isSubagent: boolean;
	    ts: string;
	    resultSize: number;
	    resultIsError: boolean;
	    episodeId: number;
	
	    static createFrom(source: any = {}) {
	        return new ToolCall(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.messageId = source["messageId"];
	        this.seq = source["seq"];
	        this.toolUseId = source["toolUseId"];
	        this.name = source["name"];
	        this.inputJson = source["inputJson"];
	        this.filePath = source["filePath"];
	        this.isModifying = source["isModifying"];
	        this.isRead = source["isRead"];
	        this.isBash = source["isBash"];
	        this.isSearch = source["isSearch"];
	        this.isSubagent = source["isSubagent"];
	        this.ts = source["ts"];
	        this.resultSize = source["resultSize"];
	        this.resultIsError = source["resultIsError"];
	        this.episodeId = source["episodeId"];
	    }
	}
	export class Watermark {
	    id: number;
	    sessionId: string;
	    messageUuid: string;
	    seq: number;
	    label: string;
	    note: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Watermark(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sessionId = source["sessionId"];
	        this.messageUuid = source["messageUuid"];
	        this.seq = source["seq"];
	        this.label = source["label"];
	        this.note = source["note"];
	        this.createdAt = source["createdAt"];
	    }
	}

}

