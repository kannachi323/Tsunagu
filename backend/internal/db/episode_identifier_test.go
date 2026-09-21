package db

import (
	"path/filepath"
	"testing"
)

func TestRepairAnimeEpisodeIdentifiers(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec(`
 INSERT INTO repositories(id,index_url) VALUES(1,'https://repo.example/index.json');
 INSERT INTO extensions(id,repository_id,package_name,name,version,content_type,lang,apk_url) VALUES
 (1,1,'eu.kanade.tachiyomi.animeextension.en.onetwothreeanime','123Anime','14.2','anime','en','https://repo.example/source.apk'),
 (2,1,'other.source','Other','1','anime','en','https://repo.example/other.apk');
 INSERT INTO media(id,extension_id,external_id,content_type,title) VALUES
 (1,1,'/anime/kanojo-okarishimasu-5th-season','anime','Series'),
 (2,2,'/anime/another','anime','Other');
 INSERT INTO chapters(id,media_id,external_id) VALUES
 (1,1,'/1'),(2,1,'/2'),(3,1,'kanojo-okarishimasu-5th-season/2'),
 (4,1,'/episode-3'),(5,2,'/1');
 INSERT INTO reading_progress(media_id,chapter_id,progress,completed) VALUES(1,1,0.5,0);
 `)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = conn.Exec(migration0019); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[int]string{1: "kanojo-okarishimasu-5th-season/1", 2: "/2", 3: "kanojo-okarishimasu-5th-season/2", 4: "/episode-3", 5: "/1"} {
		var got string
		if err = conn.QueryRow("SELECT external_id FROM chapters WHERE id=?", id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("chapter %d: %q, want %q", id, got, want)
		}
	}
	var progress float64
	if err = conn.QueryRow("SELECT progress FROM reading_progress WHERE chapter_id=1").Scan(&progress); err != nil || progress != 0.5 {
		t.Fatalf("progress was not preserved: %v %v", progress, err)
	}
}
