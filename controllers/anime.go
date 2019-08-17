package controllers

import (
	"fmt"

	"anime/models"

	"github.com/astaxie/beego/orm"
)

type AnimeController struct {
	BaseController
}

func (c *AnimeController)Anime()  {
	name:=c.GetString(":name")
	m:=&models.Anime{Name:name}
	orm.NewOrm().Read(m,"name")
	fmt.Println(m)
	c.TplName="id.html"
}

func (c *AnimeController)Search() {
	c.TplName="index.html"
	
	name:=c.GetString("name")
	if name == "" {
		c.Redirect("/",302)
	}
	var ms []*models.Anime
	orm.NewOrm().QueryTable("Anime").Filter("name__icontains",name).All(&ms)
	fmt.Println(ms)
	
	c.Data["animes"]=ms
	c.Data["search"]=name
}