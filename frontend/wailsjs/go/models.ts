export namespace main {
	
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

}

