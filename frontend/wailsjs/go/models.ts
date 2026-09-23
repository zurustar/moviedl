export namespace main {
	
	export class AddResult {
	    id: string;
	    reason: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new AddResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.reason = source["reason"];
	        this.message = source["message"];
	    }
	}
	export class PlaylistEntry {
	    id: string;
	    url: string;
	    title: string;
	    thumbnail: string;
	    duration: string;
	
	    static createFrom(source: any = {}) {
	        return new PlaylistEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.url = source["url"];
	        this.title = source["title"];
	        this.thumbnail = source["thumbnail"];
	        this.duration = source["duration"];
	    }
	}
	export class UpdateImpact {
	    items: number;
	    otherProcs: number;
	
	    static createFrom(source: any = {}) {
	        return new UpdateImpact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = source["items"];
	        this.otherProcs = source["otherProcs"];
	    }
	}

}

