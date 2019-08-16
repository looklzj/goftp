package routers

import (
	"anime/controllers"
	"github.com/astaxie/beego"
	
)

func init() {
	beego.Router("/",&controllers.HomeController{},"GET:Home" )
	beego.Router("/:name",&controllers.AnimeController{},"GET:Anime" )
}
