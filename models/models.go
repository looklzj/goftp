package models

import (
	"github.com/astaxie/beego"
	"github.com/astaxie/beego/orm"
	_ "github.com/go-sql-driver/mysql"
)

type Anime struct {
	Id int 
	Img string 	//图片
	Name string	  //名称
	Count int //集数
	Tag *Tag `orm:"rel(fk)"`
} 

type Tag struct {
	Id int 
	Name string
	Anime []*Anime  `orm:"reverse(many)"`
}

type Unit struct {
	Id int
	Img string 
	Name string
	PlayUrl string
}

func init() {
	if err:=orm.RegisterDataBase("default","mysql","root:rootroot@tcp(127.0.0.1:3306)/anime?charset=utf8");err!=nil{
		beego.Info("注册数据库失败")
	}

	orm.RegisterModel(new(Anime),new(Tag),new(Unit))
	orm.RunSyncdb("default",false,true)
}
